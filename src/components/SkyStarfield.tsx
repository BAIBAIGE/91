import type { CSSProperties } from "react";
import { useCurrentTheme } from "@/lib/useCurrentTheme";

/**
 * 星空蓝主题专属：视口级星星贴纸层。
 *
 * 用 vip.215.im 那套动画 GIF 贴纸：每个 GIF 自带逐帧闪烁动画，
 * 比 CSS opacity 呼吸真实得多。桌面和手机分开维护点位，避免首屏密度
 * 被页面高度拉伸，也避免手机端星星过大。
 *
 * - 资源在 public/stickers/star-*.gif，会被打包到 dist/stickers/
 * - 渲染在 App 根节点，主站和后台都看得到
 * - data-theme!=="sky" 时不创建图片节点，避免下载仅 sky 主题使用的 GIF
 * - aria-hidden + pointer-events: none，对可访问性和点击都透明
 * - 加 / 减 / 调星只动 DESKTOP_STARS / MOBILE_STARS 数组
 */

const STICKERS = [
  "/stickers/star-gold.gif",
  "/stickers/star-pink.gif",
  "/stickers/star-sparkle.gif",
  "/stickers/star-mini.gif",
];

type StarSpec = {
  /** 锚点用百分号写，CSS 直接当 top/left/right/bottom 用 */
  top?: string;
  bottom?: string;
  left?: string;
  right?: string;
  /** 像素，控制 GIF 渲染尺寸 */
  size: number;
};

/**
 * 桌面：星星偏四周和顶部，主体阅读区保持干净。
 * 大星只放边角，小星补顶部和侧边空隙。
 */
const DESKTOP_STARS: StarSpec[] = [
  { top: "6%", left: "5%", size: 44 },
  { top: "4%", left: "24%", size: 26 },
  { top: "8%", right: "12%", size: 48 },
  { top: "17%", right: "31%", size: 30 },
  { top: "24%", left: "8%", size: 34 },
  { top: "28%", right: "5%", size: 38 },
  { top: "43%", left: "3%", size: 24 },
  { top: "49%", right: "9%", size: 28 },
  { top: "63%", left: "11%", size: 32 },
  { top: "66%", right: "18%", size: 44 },
  { bottom: "14%", left: "5%", size: 36 },
  { bottom: "10%", right: "6%", size: 42 },
  { bottom: "4%", left: "33%", size: 24 },
  { bottom: "6%", right: "34%", size: 28 },
  { top: "13%", left: "52%", size: 22 },
  { bottom: "24%", right: "41%", size: 22 },
];

/**
 * 手机：数量更少、尺寸更小，只做边缘点缀。
 */
const MOBILE_STARS: StarSpec[] = [
  { top: "7%", left: "6%", size: 30 },
  { top: "11%", right: "7%", size: 28 },
  { top: "24%", right: "3%", size: 22 },
  { top: "39%", left: "4%", size: 22 },
  { top: "57%", right: "6%", size: 26 },
  { bottom: "23%", left: "9%", size: 24 },
  { bottom: "12%", right: "12%", size: 30 },
  { bottom: "5%", left: "48%", size: 20 },
];

export function SkyStarfield() {
  const theme = useCurrentTheme();

  if (theme !== "sky") {
    return null;
  }

  return (
    <div className="sky-starfield" aria-hidden="true">
      {DESKTOP_STARS.map((s, i) => {
        const style: CSSProperties = {
          top: s.top,
          bottom: s.bottom,
          left: s.left,
          right: s.right,
          width: s.size,
          height: s.size,
        };
        const src = STICKERS[i % STICKERS.length];
        return (
          <img
            key={`desktop-${i}`}
            className="sky-star sky-star--desktop"
            src={src}
            alt=""
            style={style}
          />
        );
      })}
      {MOBILE_STARS.map((s, i) => {
        const style: CSSProperties = {
          top: s.top,
          bottom: s.bottom,
          left: s.left,
          right: s.right,
          width: s.size,
          height: s.size,
        };
        const src = STICKERS[(i + 1) % STICKERS.length];
        return (
          <img
            key={`mobile-${i}`}
            className="sky-star sky-star--mobile"
            src={src}
            alt=""
            style={style}
          />
        );
      })}
    </div>
  );
}
