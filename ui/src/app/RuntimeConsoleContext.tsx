import { createContext, useContext, type ReactNode } from "react";

import { useRuntimeConsole, type RuntimeConsoleViewModel } from "./useRuntimeConsole";

// The shim's external surface, exposed through context so workspace
// views consume it directly instead of receiving it as a
// {state, actions} prop bag drilled through AppShell. Identifiers
// inside the views are unchanged — the destructure at each view's
// top reads the same shape the prop bag did.
export const RuntimeConsoleContext = createContext<RuntimeConsoleViewModel | null>(null);

export function RuntimeConsoleContextProvider({ children }: { children: ReactNode }) {
  const value = useRuntimeConsole();
  return <RuntimeConsoleContext.Provider value={value}>{children}</RuntimeConsoleContext.Provider>;
}

export function useRuntimeConsoleContext(): RuntimeConsoleViewModel {
  const ctx = useContext(RuntimeConsoleContext);
  if (!ctx) {
    throw new Error(
      "useRuntimeConsoleContext must be used inside <RuntimeConsoleContextProvider>",
    );
  }
  return ctx;
}
