const maxIdLength = 64;

/**
 * Turns a --name into the workspace id ws derives from it (ADR 0012): fold
 * accents, keep lowercase ASCII letters and digits, and replace every other
 * run of characters with one "-".
 */
export const sanitizeName = (name: string): string => {
  const folded = name.normalize("NFKD").replace(/\p{Mn}/gu, "").toLowerCase();
  const id = folded.replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  return id.length > maxIdLength ? id.slice(0, maxIdLength).replace(/-+$/, "") : id;
};

const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789";

export const randomSuffix = (random: () => number = Math.random): string =>
  Array.from({ length: 6 }, () => alphabet[Math.floor(random() * alphabet.length)] ?? "x").join("");
