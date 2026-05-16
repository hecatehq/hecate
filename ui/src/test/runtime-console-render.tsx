import type { ReactElement, ReactNode } from "react";

import { RuntimeConsoleContext } from "../app/RuntimeConsoleContext";
import type { RuntimeConsoleViewModel } from "../app/useRuntimeConsole";

// Wraps a render target in <RuntimeConsoleContext.Provider> so views
// that read state/actions through useRuntimeConsoleContext() see a
// fixture instead of the real hook. Used by per-view tests where the
// component-under-test used to take `state` + `actions` as props.
export function withRuntimeConsole(
  ui: ReactElement,
  ctx: { state: RuntimeConsoleViewModel["state"]; actions: RuntimeConsoleViewModel["actions"] },
): ReactNode {
  return <RuntimeConsoleContext.Provider value={ctx}>{ui}</RuntimeConsoleContext.Provider>;
}
