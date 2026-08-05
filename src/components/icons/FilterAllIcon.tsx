import type { SVGProps } from "react";

type FilterAllIconProps = SVGProps<SVGSVGElement> & {
  size?: number;
};

// Matches CPA's authentication-file "all providers" filter icon (Lucide, ISC).
export function FilterAllIcon({ size = 20, ...props }: FilterAllIconProps) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      <rect x="3.5" y="3.5" width="5" height="5" rx="1.4" />
      <rect x="15.5" y="3.5" width="5" height="5" rx="1.4" />
      <rect x="3.5" y="15.5" width="5" height="5" rx="1.4" />
      <rect x="15.5" y="15.5" width="5" height="5" rx="1.4" />
      <path d="M8.5 8.5 10.75 10.75" />
      <path d="M15.5 8.5 13.25 10.75" />
      <path d="M8.5 15.5 10.75 13.25" />
      <path d="M15.5 15.5 13.25 13.25" />
      <circle cx="12" cy="12" r="1.6" fill="currentColor" stroke="none" />
    </svg>
  );
}
