import { ThemeProvider } from "@mui/material";
import { render, type RenderResult } from "@testing-library/react";
import type { ReactElement } from "react";
import { Provider } from "react-redux";
import type { AppStore } from "../store";
import { theme } from "../theme";

export const renderWithStore = (ui: ReactElement, store: AppStore): RenderResult =>
  render(
    <Provider store={store}>
      <ThemeProvider theme={theme}>{ui}</ThemeProvider>
    </Provider>,
  );
