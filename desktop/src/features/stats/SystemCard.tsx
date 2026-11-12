import { useId, type JSX } from "react";
import type { HostStats } from "../../api";
import { formatBytes, formatPercentValue, usedRatio } from "./format";
import { Meter } from "./Meter";
import styles from "./StatsPane.module.css";

const dash = "—";

/** CPU, memory and disk of this host. */
export const SystemCard = ({ system }: { readonly system: HostStats }): JSX.Element => {
  const headingId = useId();
  const load = system.loadAverage === null ? dash : system.loadAverage.map((value) => value.toFixed(2)).join(" ");
  const cpuDetail = `${formatPercentValue(system.cpuPercent)} · ${system.cpuCount ?? dash} cores · load ${load}`;
  const memoryDetail =
    system.memoryUsedBytes === null || system.memoryTotalBytes === null
      ? dash
      : `${formatBytes(system.memoryUsedBytes)} of ${formatBytes(system.memoryTotalBytes)}`;
  const disk = system.disk;
  const diskDetail = disk === null ? dash : `${formatBytes(disk.usedBytes)} of ${formatBytes(disk.totalBytes)}`;
  return (
    <section className={styles.card} aria-labelledby={headingId}>
      <h2 id={headingId} className={styles.cardTitle}>
        System
      </h2>
      <Meter label="CPU" ratio={system.cpuPercent === null ? null : system.cpuPercent / 100} detail={cpuDetail} />
      <Meter
        label="Memory"
        ratio={usedRatio(system.memoryUsedBytes, system.memoryTotalBytes)}
        detail={memoryDetail}
      />
      <Meter label="Disk" ratio={usedRatio(disk?.usedBytes ?? null, disk?.totalBytes ?? null)} detail={diskDetail} />
      {disk !== null && (
        <p className={styles.path} title={disk.path}>
          {disk.path}
        </p>
      )}
    </section>
  );
};
