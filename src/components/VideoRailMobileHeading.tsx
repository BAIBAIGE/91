import { ListCollapse } from "lucide-react";

/** 手机端推荐栏在真实内容和整页骨架之间共享的稳定标题。 */
export function VideoRailMobileHeading() {
  return (
    <header className="vd-rail__head vd-rail__head--mobile-only">
      <ListCollapse className="vd-rail__head-icon" aria-hidden="true" />
      <h2 className="vd-rail__head-title">推荐视频</h2>
    </header>
  );
}
