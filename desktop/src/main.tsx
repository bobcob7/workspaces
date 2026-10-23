import "@xterm/xterm/css/xterm.css";
import "./index.css";
import { CssBaseline, ThemeProvider } from "@mui/material";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Provider } from "react-redux";
import { api } from "./api";
import { App } from "./App";
import { TerminalRegistryContext } from "./features/terminals/context";
import { TerminalRegistry } from "./features/terminals/registry";
import { createStore } from "./store";
import { theme } from "./theme";

const terminals = new TerminalRegistry(api);
const store = createStore({ api, terminals });
const root = document.getElementById("root");
if (root === null) {
  throw new Error("missing #root");
}
createRoot(root).render(
  <StrictMode>
    <Provider store={store}>
      <ThemeProvider theme={theme}>
        <CssBaseline />
        <TerminalRegistryContext.Provider value={terminals}>
          <App />
        </TerminalRegistryContext.Provider>
      </ThemeProvider>
    </Provider>
  </StrictMode>,
);
