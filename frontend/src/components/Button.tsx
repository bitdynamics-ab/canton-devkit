// The one button system for the Web UI (visuals in index.css under
// .bd-btn). Four variants with a strict usage contract:
//
//   primary   — THE one dominant action of a view or dialog (cobalt
//               fill, ink text). At most one visible per context.
//   secondary — the default: bordered, quiet (Refresh, Pause, Mint…).
//   ghost     — low-emphasis inline actions (Edit, close ×, chips).
//   danger    — destructive and irreversible only (Down, Burn,
//               Scrub, force-restore). Filled red; use sparingly —
//               recoverable actions like Stop/Pause stay secondary.
//
// Sizes: sm 28px (row/table actions — the console default) and
// md 36px (forms, dialog footers).

import type {
  CSSProperties,
  MouseEventHandler,
  ReactNode,
} from "react";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";
export type ButtonSize = "sm" | "md";

interface ButtonProps {
  variant?: ButtonVariant;
  size?: ButtonSize;
  /** Icon slot — pass an icons.tsx glyph; it inherits text color. */
  icon?: ReactNode;
  disabled?: boolean;
  fullWidth?: boolean;
  type?: "button" | "submit";
  title?: string;
  "aria-label"?: string;
  onClick?: MouseEventHandler<HTMLButtonElement>;
  style?: CSSProperties;
  children?: ReactNode;
}

export function Button({
  variant = "secondary",
  size = "sm",
  icon,
  disabled = false,
  fullWidth = false,
  type = "button",
  title,
  onClick,
  style,
  children,
  ...rest
}: ButtonProps) {
  const cls = [
    "bd-btn",
    `bd-btn--${variant}`,
    `bd-btn--${size}`,
    fullWidth ? "bd-btn--full" : "",
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <button
      type={type}
      className={cls}
      disabled={disabled}
      title={title}
      onClick={onClick}
      style={style}
      aria-label={rest["aria-label"]}
    >
      {icon ? <span className="bd-btn__icon">{icon}</span> : null}
      {children}
    </button>
  );
}
