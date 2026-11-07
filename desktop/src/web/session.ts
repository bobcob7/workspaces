export interface Session {
  readonly authenticated: boolean;
  readonly username: string | null;
  readonly version: string;
}

export type LoginResult = "ok" | "bad_credentials" | "rate_limited" | "error";

export const getSession = async (): Promise<Session> => {
  const res = await fetch("/api/session", { credentials: "same-origin" });
  if (!res.ok) {
    throw new Error(`session: status ${res.status}`);
  }
  return (await res.json()) as Session;
};

const loginStatus = (status: number): LoginResult => {
  if (status === 204) {
    return "ok";
  }
  if (status === 401) {
    return "bad_credentials";
  }
  if (status === 429) {
    return "rate_limited";
  }
  return "error";
};

export const login = async (username: string, password: string): Promise<LoginResult> => {
  try {
    const res = await fetch("/api/login", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
    return loginStatus(res.status);
  } catch {
    return "error";
  }
};

export const logout = async (): Promise<void> => {
  try {
    await fetch("/api/logout", { method: "POST", credentials: "same-origin" });
  } finally {
    location.reload();
  }
};
