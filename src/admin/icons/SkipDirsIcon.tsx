type SkipDirsIconProps = {
  size?: number;
  className?: string;
};

// Folder shape from Font Awesome Pro 7.3.1 by @fontawesome, with a custom exclusion mark.
// https://fontawesome.com/license - Commercial License, Copyright 2026 Fonticons, Inc.
export function SkipDirsIcon({ size = 20, className }: SkipDirsIconProps) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      width={size}
      height={size}
      viewBox="0 0 640 640"
      aria-hidden="true"
      focusable="false"
    >
      <path
        fill="currentColor"
        fillRule="evenodd"
        d="M512 512L128 512C92.7 512 64 483.3 64 448L64 160C64 124.7 92.7 96 128 96L266.7 96C280.5 96 294 100.5 305.1 108.8L343.5 137.6C349 141.8 355.8 144 362.7 144L512 144C547.3 144 576 172.7 576 208L576 448C576 483.3 547.3 512 512 512zM273 239C263.6 229.6 248.4 229.6 239 239C229.6 248.4 229.6 263.6 239 273L286 320L239 367C229.6 376.4 229.6 391.6 239 401C248.4 410.4 263.6 410.4 273 401L320 354L367 401C376.4 410.4 391.6 410.4 401 401C410.4 391.6 410.4 376.4 401 367L354 320L401 273C410.4 263.6 410.4 248.4 401 239C391.6 229.6 376.4 229.6 367 239L320 286L273 239z"
      />
    </svg>
  );
}
