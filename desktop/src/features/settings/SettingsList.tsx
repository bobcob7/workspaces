import type { JSX } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { groupChosen, type SettingsGroup } from "./settingsSlice";
import styles from "./SettingsList.module.css";

/** Groups with settings, in list order. General, Agents, and About join once they have some. */
const groups: readonly { readonly id: SettingsGroup; readonly title: string }[] = [{ id: "reviews", title: "Reviews" }];

export const SettingsList = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const current = useAppSelector((s) => s.settings.group);
  return (
    <nav className={styles.root} aria-label="Settings groups">
      <ul className={styles.list}>
        {groups.map((group) => (
          <li key={group.id}>
            <button
              type="button"
              className={styles.item}
              aria-current={group.id === current ? "true" : undefined}
              onClick={() => dispatch(groupChosen(group.id))}
            >
              {group.title}
            </button>
          </li>
        ))}
      </ul>
    </nav>
  );
};
