import { Component, type ErrorInfo, type ReactNode } from "react";

type Props = { children: ReactNode };
type State = { error: Error | null };

export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  override componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("[ErrorBoundary]", error, info.componentStack);
  }

  override render() {
    if (this.state.error) {
      return (
        <div className="console-root">
          <div className="console-layout console-layout--single">
            <div className="console-surface error-boundary__surface">
              <div className="stack-md">
                <div className="stack-sm">
                  <p className="console-eyebrow">Runtime error</p>
                  <h2 className="console-section__title">Something went wrong</h2>
                </div>
                <p className="body-muted">
                  {this.state.error.message || "An unexpected error occurred."}
                </p>
                <button
                  className="toolbar-button toolbar-button--primary"
                  onClick={() => window.location.reload()}
                  type="button"
                >
                  Reload
                </button>
              </div>
            </div>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
