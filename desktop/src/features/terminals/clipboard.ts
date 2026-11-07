const URL_PATTERN = /https?:\/\/[^\s<>"'`)\]}]+/g;
const TRAILING_PUNCTUATION = /[.,;:!?]+$/;

/** Every distinct http(s) URL in text, in order. A URL ends at whitespace, a bracket or a quote. */
export const extractUrls = (text: string): readonly string[] => {
  const urls = (text.match(URL_PATTERN) ?? []).map((url) => url.replace(TRAILING_PUNCTUATION, ""));
  return [...new Set(urls)];
};

/** Writes text to the system clipboard; false when the browser refuses. */
export const copyText = async (text: string): Promise<boolean> => {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
};

/** Reads the system clipboard; null when the API is missing or the browser denies it. */
export const readClipboard = async (): Promise<string | null> => {
  try {
    return await navigator.clipboard.readText();
  } catch {
    return null;
  }
};
