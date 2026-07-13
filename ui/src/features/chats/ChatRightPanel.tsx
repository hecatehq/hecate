import { useRef } from "react";
import type { ReactNode } from "react";

const MIN_WIDTH = 320;
const MAX_WIDTH = 560;
const KEYBOARD_RESIZE_STEP = 8;

export function ChatRightPanel({
  ariaLabel,
  children,
  className,
  onWidthChange,
  width,
}: {
  ariaLabel: string;
  children: ReactNode;
  className?: string;
  onWidthChange: (width: number) => void;
  width: number;
}) {
  const dragRef = useRef<{ startX: number; startWidth: number } | null>(null);

  function resizeLimit(): number {
    if (typeof window === "undefined") return MAX_WIDTH;
    return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.floor(window.innerWidth * 0.58)));
  }

  function updateWidth(nextWidth: number) {
    onWidthChange(Math.min(resizeLimit(), Math.max(MIN_WIDTH, Math.round(nextWidth))));
  }

  return (
    <aside
      aria-label={ariaLabel}
      className={className}
      style={{
        background: "var(--bg1)",
        borderLeft: "1px solid var(--border)",
        display: "flex",
        flexDirection: "column",
        flexShrink: 0,
        height: "100%",
        maxWidth: resizeLimit(),
        minHeight: 0,
        minWidth: MIN_WIDTH,
        overflow: "hidden",
        position: "relative",
        width,
      }}
    >
      <div
        aria-label="Resize right panel"
        role="separator"
        aria-orientation="vertical"
        tabIndex={0}
        className="chat-right-panel-resize-handle"
        onKeyDown={(event) => {
          if (event.key === "ArrowLeft") {
            event.preventDefault();
            updateWidth(width + KEYBOARD_RESIZE_STEP);
          }
          if (event.key === "ArrowRight") {
            event.preventDefault();
            updateWidth(width - KEYBOARD_RESIZE_STEP);
          }
        }}
        onPointerDown={(event) => {
          event.preventDefault();
          dragRef.current = { startX: event.clientX, startWidth: width };
          event.currentTarget.setPointerCapture?.(event.pointerId);
        }}
        onPointerMove={(event) => {
          const drag = dragRef.current;
          if (!drag) return;
          updateWidth(drag.startWidth + drag.startX - event.clientX);
        }}
        onPointerUp={(event) => {
          dragRef.current = null;
          event.currentTarget.releasePointerCapture?.(event.pointerId);
        }}
      />
      {children}
    </aside>
  );
}
