import { Alert, Button, TextField } from "@mui/material";
import { useState, type FormEvent, type JSX } from "react";
import { login, type LoginResult } from "./session";
import styles from "./LoginPage.module.css";

const messages: Record<Exclude<LoginResult, "ok">, string> = {
  bad_credentials: "Wrong username or password.",
  rate_limited: "Too many attempts. Try again later.",
  error: "Couldn't reach the server.",
};

/** Asks for the username and password the server was set up with. */
export const LoginPage = ({ onLoggedIn }: { readonly onLoggedIn: () => void }): JSX.Element => {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const submit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    setSubmitting(true);
    const result = await login(username, password);
    setSubmitting(false);
    if (result === "ok") {
      onLoggedIn();
      return;
    }
    setError(messages[result]);
  };
  return (
    <main className={styles.root}>
      <form className={styles.card} onSubmit={(event) => void submit(event)}>
        <img className={styles.icon} src="/icons/icon-192.png" alt="" width={56} height={56} />
        <h1 className={styles.title}>Workspaces</h1>
        {error !== null && <Alert severity="error">{error}</Alert>}
        <TextField
          label="Username"
          autoComplete="username"
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          fullWidth
        />
        <TextField
          label="Password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          fullWidth
        />
        <Button type="submit" variant="contained" disabled={submitting} fullWidth>
          Log in
        </Button>
      </form>
    </main>
  );
};
