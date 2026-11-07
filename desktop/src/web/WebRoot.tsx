import { CircularProgress } from "@mui/material";
import { useEffect, useState, type JSX, type ReactNode } from "react";
import { getSession } from "./session";
import { LoginPage } from "./LoginPage";
import styles from "./WebRoot.module.css";

type Phase = "loading" | "anonymous" | "authenticated";

/** Shows the login page until the server says the browser is signed in. */
export const WebRoot = ({ children }: { readonly children: ReactNode }): JSX.Element => {
  const [phase, setPhase] = useState<Phase>("loading");
  useEffect(() => {
    let active = true;
    const decide = (authenticated: boolean): void => {
      if (active) {
        setPhase(authenticated ? "authenticated" : "anonymous");
      }
    };
    getSession().then(
      (session) => decide(session.authenticated),
      () => decide(false),
    );
    const expired = (): void => decide(false);
    window.addEventListener("ws-online:unauthenticated", expired);
    return () => {
      active = false;
      window.removeEventListener("ws-online:unauthenticated", expired);
    };
  }, []);
  if (phase === "loading") {
    return (
      <div className={styles.loading}>
        <CircularProgress aria-label="Loading" />
      </div>
    );
  }
  if (phase === "anonymous") {
    return <LoginPage onLoggedIn={() => setPhase("authenticated")} />;
  }
  return <>{children}</>;
};
