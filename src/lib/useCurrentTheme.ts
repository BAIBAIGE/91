import { useEffect, useState } from "react";
import { getCurrentTheme, type Theme } from "@/lib/theme";

/**
 * 把 <html data-theme> 这个全站主题源同步为 React 状态。
 *
 * 主题可能在首屏服务端同步或后台切换时发生变化，因此消费方不能只在挂载时
 * 读取一次。effect 启动后立即再同步一次，避免属性恰好在首次渲染与 observer
 * 建立之间变化时漏掉更新。
 */
export function useCurrentTheme(): Theme {
  const [theme, setTheme] = useState<Theme>(getCurrentTheme);

  useEffect(() => {
    const root = document.documentElement;
    const syncTheme = () => setTheme(getCurrentTheme());
    const observer = new MutationObserver(syncTheme);

    observer.observe(root, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    syncTheme();

    return () => observer.disconnect();
  }, []);

  return theme;
}
