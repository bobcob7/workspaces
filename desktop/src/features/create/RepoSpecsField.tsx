import { Autocomplete, Chip, TextField } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import { api } from "../../api";

export interface RepoSpecsFieldProps {
  readonly value: readonly string[];
  readonly onChange: (specs: readonly string[]) => void;
}

/** Repo specs, suggested from the cache; any spec ws accepts can be typed. */
export const RepoSpecsField = ({ value, onChange }: RepoSpecsFieldProps): JSX.Element => {
  const [cached, setCached] = useState<readonly string[]>([]);
  useEffect(() => {
    let live = true;
    api
      .cacheRepos()
      .then((ids) => {
        if (live) {
          setCached(ids.map((id) => id.replace(/^github\.com\//, "")));
        }
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, []);
  return (
    <Autocomplete
      multiple
      freeSolo
      options={cached}
      value={[...value]}
      onChange={(_event, specs) => onChange(specs)}
      renderValue={(specs, getItemProps) =>
        specs.map((spec, index) => {
          const { key, ...props } = getItemProps({ index });
          return <Chip key={key} size="small" label={spec} {...props} />;
        })
      }
      renderInput={(params) => (
        <TextField
          {...params}
          label="Repositories"
          placeholder={value.length === 0 ? "repo, owner/repo, or repo@ref; none for an empty workspace" : ""}
          helperText="Press Enter after each spec. Add @branch, @tag, or @commit to check one out."
        />
      )}
    />
  );
};
