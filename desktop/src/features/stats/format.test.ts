import { describe, expect, it } from "vitest";
import { entry } from "../../test/fixtures";
import {
  formatBytes,
  formatCount,
  formatMinutes,
  formatPercent,
  formatPercentValue,
  formatRate,
  formatUsd,
  usedRatio,
  workspaceCounts,
} from "./format";

describe("formatCount", () => {
  it("keeps numbers under a thousand as they are", () => {
    expect(formatCount(999)).toBe("999");
  });

  it("drops a trailing .0 when scaling to thousands", () => {
    expect(formatCount(12_000)).toBe("12k");
  });

  it("shows one decimal when scaling to thousands", () => {
    expect(formatCount(12_300)).toBe("12.3k");
  });

  it("scales to millions", () => {
    expect(formatCount(1_200_000)).toBe("1.2M");
  });

  it("scales to billions", () => {
    expect(formatCount(3_400_000_000)).toBe("3.4B");
  });

  it("moves up a unit when rounding reaches the next one", () => {
    expect(formatCount(999_960)).toBe("1M");
  });
});

describe("formatPercent", () => {
  it("rounds a ratio to a whole percentage", () => {
    expect(formatPercent(0.934)).toBe("93%");
  });

  it("shows a dash when there is no ratio", () => {
    expect(formatPercent(null)).toBe("—");
  });
});

describe("formatPercentValue", () => {
  it("rounds a percentage to a whole number", () => {
    expect(formatPercentValue(23.44)).toBe("23%");
  });

  it("shows a dash when there is no percentage", () => {
    expect(formatPercentValue(null)).toBe("—");
  });
});

describe("formatUsd", () => {
  it("shows zero with cents", () => {
    expect(formatUsd(0)).toBe("$0.00");
  });

  it("shows a sub-cent amount as less than a cent", () => {
    expect(formatUsd(0.004)).toBe("<$0.01");
  });

  it("shows amounts under a hundred dollars with cents", () => {
    expect(formatUsd(4.123)).toBe("$4.12");
  });

  it("shows amounts of a hundred dollars or more without cents", () => {
    expect(formatUsd(123.4)).toBe("$123");
  });
});

describe("formatBytes", () => {
  it("shows small sizes in bytes", () => {
    expect(formatBytes(900)).toBe("900 B");
  });

  it("shows megabytes without a decimal when whole", () => {
    expect(formatBytes(512 * 1024 * 1024)).toBe("512 MB");
  });

  it("shows gigabytes with one decimal", () => {
    expect(formatBytes(12.3 * 1024 ** 3)).toBe("12.3 GB");
  });
});

describe("formatMinutes", () => {
  it("shows under a minute as less than a minute", () => {
    expect(formatMinutes(0.4)).toBe("<1 min");
  });

  it("shows minutes under an hour", () => {
    expect(formatMinutes(15.2)).toBe("15 min");
  });

  it("shows hours and minutes", () => {
    expect(formatMinutes(125)).toBe("2 h 5 min");
  });

  it("shows whole hours without minutes", () => {
    expect(formatMinutes(120)).toBe("2 h");
  });
});

describe("formatRate", () => {
  it("rounds to the given digits", () => {
    expect(formatRate(1.234, 2)).toBe("1.23");
  });

  it("shows a dash when there is no value", () => {
    expect(formatRate(null, 2)).toBe("—");
  });
});

describe("usedRatio", () => {
  it("divides used by total", () => {
    expect(usedRatio(5, 10)).toBe(0.5);
  });

  it("is null when the used amount is unknown", () => {
    expect(usedRatio(null, 10)).toBeNull();
  });

  it("is null when the total is unknown", () => {
    expect(usedRatio(5, null)).toBeNull();
  });

  it("is null when the total is zero", () => {
    expect(usedRatio(5, 0)).toBeNull();
  });
});

describe("workspaceCounts", () => {
  it("counts only entries with a workspace id as workspaces", () => {
    const counts = workspaceCounts([entry({ status: "working" }), entry({ kind: "shell", workspaceId: null, status: "running" })]);
    expect(counts).toEqual({ active: 1, total: 1 });
  });

  it("counts working and needing-input workspaces as active", () => {
    const counts = workspaceCounts([
      entry({ key: "a", workspaceId: "a", status: "working" }),
      entry({ key: "b", workspaceId: "b", status: "needsInput" }),
    ]);
    expect(counts).toEqual({ active: 2, total: 2 });
  });

  it("counts starting and running workspaces as active", () => {
    const counts = workspaceCounts([
      entry({ key: "a", workspaceId: "a", status: "starting" }),
      entry({ key: "b", workspaceId: "b", status: "running" }),
    ]);
    expect(counts).toEqual({ active: 2, total: 2 });
  });

  it("does not count done workspaces as active", () => {
    expect(workspaceCounts([entry({ status: "done" })])).toEqual({ active: 0, total: 1 });
  });
});
