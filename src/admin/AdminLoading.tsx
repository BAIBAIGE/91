export function AdminLoading() {
  return (
    <div className="admin-loading" role="status" aria-label="加载中">
      <div className="lds-ellipsis" aria-hidden="true">
        <div />
        <div />
        <div />
        <div />
      </div>
    </div>
  );
}
