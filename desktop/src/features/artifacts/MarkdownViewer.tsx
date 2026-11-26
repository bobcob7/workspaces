import { Button } from "@mui/material";
import { useEffect, useMemo, useRef, useState, type JSX, type MouseEvent, type ReactNode } from "react";
import ReactMarkdown, { type Components, type ExtraProps } from "react-markdown";
import remarkGfm from "remark-gfm";
import { api, type NewAnchor, type Thread } from "../../api";
import { rehypeAnchors, selectionAnchor } from "./anchors";
import styles from "./MarkdownViewer.module.css";

const keptProtocols = ["http:", "https:", "mailto:"];

/** The link targets the viewer keeps: web and mail links, and `#` anchors. Anything else becomes an empty href. */
export const keepUrl = (url: string): string => {
  if (url.startsWith("#")) {
    return url;
  }
  try {
    return keptProtocols.includes(new URL(url).protocol) ? url : "";
  } catch {
    return "";
  }
};

const openLink = (event: MouseEvent<HTMLAnchorElement>, href: string | undefined): void => {
  event.preventDefault();
  if (href === undefined || href === "" || href.startsWith("#")) {
    return;
  }
  void api.openExternal(href).catch(() => undefined);
};

interface BlockProps extends ExtraProps {
  readonly children?: ReactNode;
  /** Set by rehypeAnchors on a block that is outlined as a whole. */
  readonly className?: string;
  readonly "data-thread"?: string;
}

/** The source lines of a block, and the highlight the anchors plugin gave it. */
const blockAttributes = ({ node, className, "data-thread": thread }: BlockProps): Record<string, string | number | undefined> => ({
  className,
  "data-thread": thread,
  "data-line-start": node?.position?.start.line,
  "data-line-end": node?.position?.end.line,
});

/** Block elements carry their source lines, so a selection can be anchored to them (ADR 0079). */
const components: Components = {
  a: ({ href, children }) => <a href={href} onClick={(event) => openLink(event, href)}>{children}</a>,
  img: ({ alt, src }) => <span>{`[image: ${alt ?? ""}](${src ?? ""})`}</span>,
  p: (props: BlockProps) => <p {...blockAttributes(props)}>{props.children}</p>,
  h1: (props: BlockProps) => <h1 {...blockAttributes(props)}>{props.children}</h1>,
  h2: (props: BlockProps) => <h2 {...blockAttributes(props)}>{props.children}</h2>,
  h3: (props: BlockProps) => <h3 {...blockAttributes(props)}>{props.children}</h3>,
  h4: (props: BlockProps) => <h4 {...blockAttributes(props)}>{props.children}</h4>,
  h5: (props: BlockProps) => <h5 {...blockAttributes(props)}>{props.children}</h5>,
  h6: (props: BlockProps) => <h6 {...blockAttributes(props)}>{props.children}</h6>,
  li: (props: BlockProps) => <li {...blockAttributes(props)}>{props.children}</li>,
  blockquote: (props: BlockProps) => <blockquote {...blockAttributes(props)}>{props.children}</blockquote>,
  pre: (props: BlockProps) => <pre {...blockAttributes(props)}>{props.children}</pre>,
  tr: (props: BlockProps) => <tr {...blockAttributes(props)}>{props.children}</tr>,
};

const remarkPlugins = [remarkGfm];

/** The nearest element under `article` that carries its source lines. */
const blockAround = (node: Node | null, article: HTMLElement): HTMLElement | null => {
  let current: Node | null = node;
  while (current !== null && current !== article) {
    if (current instanceof HTMLElement && current.hasAttribute("data-line-start")) {
      return current;
    }
    current = current.parentNode;
  }
  return null;
};

interface PendingComment {
  readonly anchor: NewAnchor;
  readonly wholeBlock: boolean;
  readonly block: HTMLElement;
  readonly top: number;
  readonly left: number;
}

const threadElement = (root: HTMLElement, thread: string): HTMLElement | null =>
  Array.from(root.querySelectorAll<HTMLElement>("[data-thread]")).find((el) => el.dataset.thread === thread) ?? null;

export interface MarkdownViewerProps {
  readonly source: string;
  /** Threads to highlight. Each must be anchored to `revision`. */
  readonly highlights: readonly Thread[];
  readonly revision: number;
  /** Starts a comment on the selection, with the selected block to open beside; null on an older revision. */
  readonly onComment: ((anchor: NewAnchor, wholeBlock: boolean, anchorEl: HTMLElement | null) => void) | null;
  readonly activeThread: string | null;
  readonly onThreadClick: (thread: string) => void;
}

/** A Markdown artifact rendered with GFM, raw HTML dropped, and its threads highlighted. */
export const MarkdownViewer = ({
  source,
  highlights,
  revision,
  onComment,
  activeThread,
  onThreadClick,
}: MarkdownViewerProps): JSX.Element => {
  const rootRef = useRef<HTMLDivElement>(null);
  const [pending, setPending] = useState<PendingComment | null>(null);
  const rehypePlugins = useMemo(() => [() => rehypeAnchors(highlights, revision)], [highlights, revision]);

  useEffect(() => {
    const root = rootRef.current;
    if (activeThread === null || root === null) {
      return;
    }
    const target = threadElement(root, activeThread);
    target?.scrollIntoView?.({ block: "center" });
    target?.setAttribute("data-focused", "");
    return () => target?.removeAttribute("data-focused");
  }, [activeThread]);

  const captureSelection = (): void => {
    const root = rootRef.current;
    const selection = window.getSelection();
    if (root === null || selection === null || selection.isCollapsed || selection.rangeCount === 0) {
      setPending(null);
      return;
    }
    const block = blockAround(selection.anchorNode, root);
    if (block === null) {
      setPending(null);
      return;
    }
    const selected = selection.toString();
    const anchor = selectionAnchor(
      source,
      Number(block.getAttribute("data-line-start")),
      Number(block.getAttribute("data-line-end")),
      selected,
    );
    const box = root.getBoundingClientRect();
    const rect = block.getBoundingClientRect();
    setPending({
      anchor,
      wholeBlock: anchor.quote !== selected.trim(),
      block,
      top: rect.bottom - box.top,
      left: rect.left - box.left,
    });
  };

  const onClick = (event: MouseEvent<HTMLElement>): void => {
    if (!(event.target instanceof Element)) {
      return;
    }
    const marked = event.target.closest<HTMLElement>("[data-thread]");
    if (marked?.dataset.thread !== undefined) {
      onThreadClick(marked.dataset.thread);
    }
  };

  return (
    <div ref={rootRef} className={styles.root}>
      <article
        className={styles.article}
        aria-label="Document"
        onMouseUp={captureSelection}
        onKeyUp={captureSelection}
        onClick={onClick}
      >
        <ReactMarkdown
          remarkPlugins={remarkPlugins}
          rehypePlugins={rehypePlugins}
          skipHtml
          urlTransform={keepUrl}
          components={components}
        >
          {source}
        </ReactMarkdown>
      </article>
      {pending !== null && onComment !== null && (
        <Button
          size="small"
          variant="contained"
          className={styles.comment}
          style={{ top: pending.top, left: pending.left }}
          onClick={() => {
            onComment(pending.anchor, pending.wholeBlock, pending.block);
            setPending(null);
          }}
        >
          Comment
        </Button>
      )}
    </div>
  );
};
