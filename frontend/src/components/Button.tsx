// Visuals in index.css under .bd-btn. Variant contract:
//   primary   — at most one dominant action per view/dialog.
//   secondary — the default (bordered, quiet).
//   ghost     — low-emphasis inline actions.
//   danger    — destructive AND irreversible only; recoverable
//               actions like Stop/Pause stay secondary.
// Sizes: sm 28px (default, row/table), md 36px (forms, dialog footers).

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
  /** Icon slot — pass an icons.tsx glyph. */
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
