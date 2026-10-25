const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789";

/** A random token for keys the app makes up, such as a new shell terminal's. */
export const randomToken = (random: () => number = Math.random): string =>
  Array.from({ length: 8 }, () => alphabet[Math.floor(random() * alphabet.length)] ?? "x").join("");
