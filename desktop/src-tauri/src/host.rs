//! The host's CPU, memory and disk for the stats (ADR 0059): /proc on
//! Linux, Mach and sysctl on macOS, statvfs and getloadavg on both.

use serde::Serialize;
use std::path::{Path, PathBuf};

/// Cumulative CPU ticks: busy and total, across all CPUs.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct CpuTicks {
    pub busy: u64,
    pub total: u64,
}

/// Size and use of the volume that holds a path.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DiskStats {
    pub path: String,
    pub total_bytes: u64,
    pub used_bytes: u64,
}

/// The host's load as the stats bar shows it; a field is None when its read failed.
#[derive(Debug, Clone, Default, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct HostStats {
    pub cpu_percent: Option<f64>,
    pub cpu_count: Option<u32>,
    pub load_average: Option<[f64; 3]>,
    pub memory_total_bytes: Option<u64>,
    pub memory_used_bytes: Option<u64>,
    pub disk: Option<DiskStats>,
}

/// Parses the first `cpu ` line of /proc/stat into busy and total ticks.
pub fn parse_proc_stat(text: &str) -> Option<CpuTicks> {
    let rest = text.lines().find_map(|line| line.strip_prefix("cpu "))?;
    let fields = rest
        .split_whitespace()
        .take(8)
        .map(|field| field.parse::<u64>().ok())
        .collect::<Option<Vec<u64>>>()?;
    let &[user, nice, system, idle, iowait, irq, softirq, steal] = fields.as_slice() else {
        return None;
    };
    let busy = [user, nice, system, irq, softirq, steal]
        .iter()
        .fold(0, |sum: u64, ticks| sum.saturating_add(*ticks));
    Some(CpuTicks {
        busy,
        total: busy.saturating_add(idle).saturating_add(iowait),
    })
}

/// Parses /proc/meminfo into (total, used) bytes, where used is MemTotal − MemAvailable.
pub fn parse_meminfo(text: &str) -> Option<(u64, u64)> {
    let total = meminfo_bytes(text, "MemTotal:")?;
    let available = meminfo_bytes(text, "MemAvailable:")?;
    Some((total, total.saturating_sub(available)))
}

fn meminfo_bytes(text: &str, key: &str) -> Option<u64> {
    let line = text.lines().find_map(|line| line.strip_prefix(key))?;
    let kib: u64 = line.split_whitespace().next()?.parse().ok()?;
    kib.checked_mul(1024)
}

/// Busy share between two samples, 0–100, rounded to one decimal; None when
/// the total didn't grow or a counter went backwards.
pub fn cpu_percent(prev: CpuTicks, now: CpuTicks) -> Option<f64> {
    let busy = now.busy.checked_sub(prev.busy)?;
    let total = now
        .total
        .checked_sub(prev.total)
        .filter(|&total| total > 0)?;
    let percent = (busy as f64 / total as f64 * 100.0).min(100.0);
    Some((percent * 10.0).round() / 10.0)
}

/// The current CPU ticks (Linux: /proc/stat; macOS: HOST_CPU_LOAD_INFO).
#[cfg(target_os = "linux")]
pub fn read_cpu_ticks() -> Option<CpuTicks> {
    let text = std::fs::read_to_string("/proc/stat").ok()?;
    parse_proc_stat(&text)
}

/// The current CPU ticks (Linux: /proc/stat; macOS: HOST_CPU_LOAD_INFO).
#[cfg(target_os = "macos")]
pub fn read_cpu_ticks() -> Option<CpuTicks> {
    // SAFETY: host_cpu_load_info is plain integers, so all-zero is a valid value.
    let mut info: libc::host_cpu_load_info = unsafe { std::mem::zeroed() };
    let mut count = libc::HOST_CPU_LOAD_INFO_COUNT;
    // SAFETY: info and count are the size host_statistics expects for HOST_CPU_LOAD_INFO.
    let status = unsafe {
        libc::host_statistics(
            libc::mach_host_self(),
            libc::HOST_CPU_LOAD_INFO,
            (&mut info as *mut libc::host_cpu_load_info).cast(),
            &mut count,
        )
    };
    if status != libc::KERN_SUCCESS {
        return None;
    }
    let ticks = info.cpu_ticks.map(u64::from);
    let state = |index: libc::c_int| ticks[index as usize];
    let busy = state(libc::CPU_STATE_USER)
        .saturating_add(state(libc::CPU_STATE_SYSTEM))
        .saturating_add(state(libc::CPU_STATE_NICE));
    Some(CpuTicks {
        busy,
        total: busy.saturating_add(state(libc::CPU_STATE_IDLE)),
    })
}

/// The current CPU ticks; None on platforms without a reader.
#[cfg(not(any(target_os = "linux", target_os = "macos")))]
pub fn read_cpu_ticks() -> Option<CpuTicks> {
    None
}

/// (total, used) memory in bytes (Linux: /proc/meminfo; macOS: hw.memsize
/// and HOST_VM_INFO64).
#[cfg(target_os = "linux")]
pub fn read_memory() -> Option<(u64, u64)> {
    let text = std::fs::read_to_string("/proc/meminfo").ok()?;
    parse_meminfo(&text)
}

/// (total, used) memory in bytes (Linux: /proc/meminfo; macOS: hw.memsize
/// and HOST_VM_INFO64).
#[cfg(target_os = "macos")]
pub fn read_memory() -> Option<(u64, u64)> {
    let mut total: u64 = 0;
    let mut size = std::mem::size_of::<u64>();
    // SAFETY: the name is NUL-terminated, and total and size describe the 8-byte output buffer.
    let status = unsafe {
        libc::sysctlbyname(
            c"hw.memsize".as_ptr(),
            (&mut total as *mut u64).cast(),
            &mut size,
            std::ptr::null_mut(),
            0,
        )
    };
    if status != 0 {
        return None;
    }
    // SAFETY: sysconf has no preconditions for a standard name.
    let page = u64::try_from(unsafe { libc::sysconf(libc::_SC_PAGESIZE) }).ok()?;
    // SAFETY: vm_statistics64 is plain integers, so all-zero is a valid value.
    let mut vm: libc::vm_statistics64 = unsafe { std::mem::zeroed() };
    let mut count = libc::HOST_VM_INFO64_COUNT;
    // SAFETY: vm and count are the size host_statistics64 expects for HOST_VM_INFO64.
    let status = unsafe {
        libc::host_statistics64(
            libc::mach_host_self(),
            libc::HOST_VM_INFO64,
            (&mut vm as *mut libc::vm_statistics64).cast(),
            &mut count,
        )
    };
    if status != libc::KERN_SUCCESS {
        return None;
    }
    let free_pages = u64::from(vm.free_count)
        .saturating_add(u64::from(vm.inactive_count))
        .saturating_add(u64::from(vm.speculative_count))
        .saturating_add(u64::from(vm.purgeable_count));
    let free = free_pages.saturating_mul(page).min(total);
    Some((total, total.saturating_sub(free)))
}

/// (total, used) memory in bytes; None on platforms without a reader.
#[cfg(not(any(target_os = "linux", target_os = "macos")))]
pub fn read_memory() -> Option<(u64, u64)> {
    None
}

/// statvfs of path: total = f_blocks × f_frsize, used = total − f_bavail × f_frsize.
#[cfg(unix)]
pub fn read_disk(path: &Path) -> Option<DiskStats> {
    use std::os::unix::ffi::OsStrExt;

    let c_path = std::ffi::CString::new(path.as_os_str().as_bytes()).ok()?;
    // SAFETY: statvfs is plain integers, so all-zero is a valid value.
    let mut stat: libc::statvfs = unsafe { std::mem::zeroed() };
    // SAFETY: c_path is NUL-terminated and stat is a valid out-pointer.
    if unsafe { libc::statvfs(c_path.as_ptr(), &mut stat) } != 0 {
        return None;
    }
    let block = widen(stat.f_frsize);
    let total = widen(stat.f_blocks).saturating_mul(block);
    let available = widen(stat.f_bavail).saturating_mul(block);
    Some(DiskStats {
        path: path.display().to_string(),
        total_bytes: total,
        used_bytes: total.saturating_sub(available),
    })
}

/// statvfs of path; None on platforms without statvfs.
#[cfg(not(unix))]
pub fn read_disk(_path: &Path) -> Option<DiskStats> {
    None
}

/// Widens a statvfs field, which is u32 on some platforms and u64 on others.
#[cfg(unix)]
fn widen<T: Into<u64>>(value: T) -> u64 {
    value.into()
}

/// The 1, 5 and 15 minute load averages from getloadavg.
#[cfg(unix)]
pub fn load_average() -> Option<[f64; 3]> {
    let mut loads = [0.0_f64; 3];
    // SAFETY: loads has room for the three samples getloadavg writes.
    let written = unsafe { libc::getloadavg(loads.as_mut_ptr(), 3) };
    (written == 3).then_some(loads)
}

/// The 1, 5 and 15 minute load averages; None on platforms without getloadavg.
#[cfg(not(unix))]
pub fn load_average() -> Option<[f64; 3]> {
    None
}

/// $WS_CONFIG_DIR when set and non-empty, else $HOME.
pub fn disk_path() -> Option<PathBuf> {
    ["WS_CONFIG_DIR", "HOME"]
        .into_iter()
        .find_map(|key| std::env::var_os(key).filter(|value| !value.is_empty()))
        .map(PathBuf::from)
}

/// Takes one sample. prev is the CPU ticks from the last call; returns the
/// stats and the ticks to keep for the next call.
pub fn sample(prev: Option<CpuTicks>, disk: Option<&Path>) -> (HostStats, Option<CpuTicks>) {
    let now = read_cpu_ticks();
    let percent = prev.zip(now).and_then(|(prev, now)| cpu_percent(prev, now));
    let memory = read_memory();
    let stats = HostStats {
        cpu_percent: percent,
        cpu_count: std::thread::available_parallelism()
            .ok()
            .and_then(|count| u32::try_from(count.get()).ok()),
        load_average: load_average(),
        memory_total_bytes: memory.map(|(total, _)| total),
        memory_used_bytes: memory.map(|(_, used)| used),
        disk: disk.and_then(read_disk),
    };
    (stats, now)
}

#[cfg(test)]
mod tests {
    use super::*;

    const PROC_STAT: &str = "cpu  10 2 3 100 5 1 1 1 0 0\ncpu0 5 1 1 50 2 0 0 0 0 0\n";
    const MEMINFO: &str =
        "MemTotal:       16384 kB\nMemFree:         1024 kB\nMemAvailable:    4096 kB\n";

    #[test]
    fn proc_stat_sums_busy_and_total_ticks() {
        assert_eq!(
            parse_proc_stat(PROC_STAT),
            Some(CpuTicks {
                busy: 18,
                total: 123
            })
        );
    }

    #[test]
    fn proc_stat_without_cpu_line_is_none() {
        assert_eq!(parse_proc_stat("intr 1 2 3\n"), None);
    }

    #[test]
    fn proc_stat_with_short_cpu_line_is_none() {
        assert_eq!(parse_proc_stat("cpu  1 2 3\n"), None);
    }

    #[test]
    fn meminfo_reports_total_and_used_bytes() {
        assert_eq!(
            parse_meminfo(MEMINFO),
            Some((16 * 1024 * 1024, 12 * 1024 * 1024))
        );
    }

    #[test]
    fn meminfo_without_available_is_none() {
        assert_eq!(parse_meminfo("MemTotal:       16384 kB\n"), None);
    }

    #[test]
    fn cpu_percent_is_busy_share_between_samples() {
        let prev = CpuTicks {
            busy: 10,
            total: 100,
        };
        let now = CpuTicks {
            busy: 60,
            total: 200,
        };
        assert_eq!(cpu_percent(prev, now), Some(50.0));
    }

    #[test]
    fn cpu_percent_rounds_to_one_decimal() {
        let prev = CpuTicks { busy: 0, total: 0 };
        let now = CpuTicks { busy: 1, total: 3 };
        assert_eq!(cpu_percent(prev, now), Some(33.3));
    }

    #[test]
    fn cpu_percent_without_total_growth_is_none() {
        let ticks = CpuTicks { busy: 5, total: 50 };
        assert_eq!(cpu_percent(ticks, ticks), None);
    }

    #[test]
    fn cpu_percent_with_backwards_counter_is_none() {
        let prev = CpuTicks {
            busy: 10,
            total: 100,
        };
        let now = CpuTicks {
            busy: 5,
            total: 200,
        };
        assert_eq!(cpu_percent(prev, now), None);
    }

    #[cfg(unix)]
    #[test]
    fn disk_total_is_at_least_used() {
        let dir = tempfile::tempdir().unwrap();
        let disk = read_disk(dir.path()).unwrap();
        assert!(
            disk.total_bytes >= disk.used_bytes,
            "total {} used {}",
            disk.total_bytes,
            disk.used_bytes
        );
    }

    #[test]
    fn first_sample_has_no_cpu_percent() {
        let (stats, _) = sample(None, None);
        assert_eq!(stats.cpu_percent, None);
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn first_sample_keeps_cpu_ticks_for_the_next_call() {
        let (_, ticks) = sample(None, None);
        assert!(ticks.is_some());
    }

    #[test]
    fn sample_with_previous_ticks_does_not_panic() {
        let prev = CpuTicks { busy: 0, total: 0 };
        let (stats, _) = sample(Some(prev), None);
        assert!(stats
            .cpu_percent
            .is_none_or(|percent| (0.0..=100.0).contains(&percent)));
    }
}
