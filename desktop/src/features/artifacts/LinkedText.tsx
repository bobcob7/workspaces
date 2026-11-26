import { Fragment, type JSX } from "react";
import { ArtifactRef } from "./ArtifactRef";
import { refsIn } from "./references";

/** Plain text with each `@artifact:` reference outside code shown as an in-app link (ADR 0084). */
export const LinkedText = ({ text }: { readonly text: string }): JSX.Element => {
  const found = refsIn(text);
  if (found.length === 0) {
    return <>{text}</>;
  }
  const parts: JSX.Element[] = [];
  let at = 0;
  for (const ref of found) {
    parts.push(<Fragment key={`t${at}`}>{text.slice(at, ref.start)}</Fragment>);
    parts.push(<ArtifactRef key={`r${ref.start}`} id={ref.id} rev={ref.rev} raw={text.slice(ref.start, ref.end)} />);
    at = ref.end;
  }
  parts.push(<Fragment key={`t${at}`}>{text.slice(at)}</Fragment>);
  return <>{parts}</>;
};
