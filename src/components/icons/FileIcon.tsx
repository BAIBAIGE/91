import type { SVGProps } from "react";

type FileIconProps = SVGProps<SVGSVGElement> & {
  size?: number;
};

// Font Awesome Free 7.3.1 by Fonticons, Inc. — https://fontawesome.com/license/free
export function FileIcon({ size = 16, ...props }: FileIconProps) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 384 512"
      width={size}
      height={size}
      fill="currentColor"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      <path d="M64 0C28.7 0 0 28.7 0 64L0 448c0 35.3 28.7 64 64 64l256 0c35.3 0 64-28.7 64-64l0-277.5c0-17-6.7-33.3-18.7-45.3L258.7 18.7C246.7 6.7 230.5 0 213.5 0L64 0zM325.5 176L232 176c-13.3 0-24-10.7-24-24L208 58.5 325.5 176z" />
    </svg>
  );
}
