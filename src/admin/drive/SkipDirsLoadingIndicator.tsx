export function SkipDirsLoadingIndicator() {
  return (
    <div
      className="admin-skipdirs-status"
      role="status"
      aria-label="加载中"
    >
      <span className="lds-ellipsis is-xs" aria-hidden="true">
        <span />
        <span />
        <span />
        <span />
      </span>
    </div>
  );
}
