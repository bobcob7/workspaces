import { useId, type JSX, type ReactNode } from "react";
import styles from "./LoopsPane.module.css";

/** A titled card in the detail view; its heading labels the region. */
export const Card = ({ title, children }: { readonly title: string; readonly children: ReactNode }): JSX.Element => {
  const id = useId();
  return (
    <section className={styles.card} aria-labelledby={id}>
      <h2 id={id} className={styles.cardTitle}>
        {title}
      </h2>
      {children}
    </section>
  );
};

/** A labelled, monospace, scrollable block of text. */
export const CodeBlock = ({ label, text }: { readonly label: string; readonly text: string }): JSX.Element => (
  <pre className={styles.code} aria-label={label} tabIndex={0}>
    {text}
  </pre>
);
