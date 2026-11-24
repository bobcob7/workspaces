/** The artifact's bytes decoded as UTF-8 text. */
export const decodeText = (dataBase64: string): string => {
  const bytes = Uint8Array.from(atob(dataBase64), (char) => char.charCodeAt(0));
  return new TextDecoder().decode(bytes);
};

/** The `src` that shows an artifact's bytes inline, without a network request. */
export const dataSource = (contentType: string, dataBase64: string): string =>
  `data:${contentType};base64,${dataBase64}`;
