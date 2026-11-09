import type { JSX } from "react";
import type { ConfigSetting } from "../../api";

/** Where a config.toml setting lives, and whether the file leaves it at its default. */
export const ConfigSource = ({ setting, path }: { readonly setting: ConfigSetting | undefined; readonly path: string }): JSX.Element => (
  <span title={path}>from config.toml{setting?.set === false ? " · default" : ""}</span>
);
