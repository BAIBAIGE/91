import { useEffect, useRef, useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import {
  ArchiveRestore,
  Film,
  HardDrive,
  Menu,
  ScrollText,
  SlidersHorizontal,
  Tags,
  Users,
  X,
} from "lucide-react";
import * as api from "./api";
import { AdminGlobalActions } from "./AdminGlobalActions";
import { useAuth } from "./AuthContext";
import { useToast } from "./ToastContext";
import { Modal } from "./Modal";
import { SpiderIcon } from "./icons/SpiderIcon";

export function AdminLayout() {
  const { logout } = useAuth();
  const location = useLocation();
  const navigate = useNavigate();
  const { show } = useToast();
  const mobileNavigationToggleRef = useRef<HTMLButtonElement>(null);
  const [checkingUpdate, setCheckingUpdate] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const [mobileNavigationOpen, setMobileNavigationOpen] = useState(false);
  const [availableUpdate, setAvailableUpdate] = useState<api.UpdateCheck | null>(null);

  useEffect(() => {
    document.title = "后台管理";
  }, []);

  useEffect(() => {
    setMobileNavigationOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    if (!mobileNavigationOpen) return;

    const root = document.documentElement;
    const body = document.body;
    root.classList.add("admin-mobile-nav-open");
    body.classList.add("admin-mobile-nav-open");

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key !== "Escape") return;
      setMobileNavigationOpen(false);
      window.requestAnimationFrame(() => mobileNavigationToggleRef.current?.focus());
    }

    function handleResize() {
      if (window.innerWidth > 768) setMobileNavigationOpen(false);
    }

    document.addEventListener("keydown", handleKeyDown);
    window.addEventListener("resize", handleResize);
    return () => {
      root.classList.remove("admin-mobile-nav-open");
      body.classList.remove("admin-mobile-nav-open");
      document.removeEventListener("keydown", handleKeyDown);
      window.removeEventListener("resize", handleResize);
    };
  }, [mobileNavigationOpen]);

  async function handleCheckUpdate() {
    if (checkingUpdate) return;
    setCheckingUpdate(true);
    try {
      const result = await api.checkUpdate();
      if (result.hasUpdate) {
        setAvailableUpdate(result);
        return;
      }
      if (result.currentVersion === "unknown") {
        show(`当前版本未知，GitHub 最新版本为 ${result.latestVersion}`, "info");
        return;
      }
      show(`当前已是最新版本：${result.currentVersion}`, "success");
    } catch {
      show("检查更新失败，请稍后重试", "error");
    } finally {
      setCheckingUpdate(false);
    }
  }

  async function handleLogout() {
    if (loggingOut) return;
    setLoggingOut(true);
    try {
      await logout();
      show("已退出登录", "success");
      navigate("/login", { replace: true });
    } catch {
      show("退出失败", "error");
    } finally {
      setLoggingOut(false);
    }
  }

  return (
    <div className="admin-shell">
      <button
        ref={mobileNavigationToggleRef}
        type="button"
        className={`admin-mobile-nav-toggle${mobileNavigationOpen ? " is-open" : ""}`}
        onClick={() => setMobileNavigationOpen((open) => !open)}
        title={mobileNavigationOpen ? "关闭后台菜单" : "打开后台菜单"}
        aria-label={mobileNavigationOpen ? "关闭后台菜单" : "打开后台菜单"}
        aria-controls="admin-navigation"
        aria-expanded={mobileNavigationOpen}
      >
        {mobileNavigationOpen ? (
          <X size={18} aria-hidden="true" />
        ) : (
          <Menu size={18} aria-hidden="true" />
        )}
      </button>
      <button
        type="button"
        className={`admin-mobile-nav-backdrop${mobileNavigationOpen ? " is-visible" : ""}`}
        onClick={() => {
          setMobileNavigationOpen(false);
          window.requestAnimationFrame(() => mobileNavigationToggleRef.current?.focus());
        }}
        aria-label="关闭后台菜单"
        aria-hidden={!mobileNavigationOpen}
        tabIndex={mobileNavigationOpen ? 0 : -1}
      />
      <aside
        id="admin-navigation"
        className={`admin-sidebar${mobileNavigationOpen ? " is-open" : ""}`}
        aria-label="后台导航"
      >
        <nav className="admin-nav" onClick={() => setMobileNavigationOpen(false)}>
          <div className="admin-nav__group">
            <span className="admin-nav__group-label">资源</span>
            <NavLink
              to="/admin/drives"
              className={({ isActive }) =>
                `admin-nav__link ${isActive ? "is-active" : ""}`
              }
            >
              <span className="admin-nav__icon" aria-hidden="true">
                <HardDrive size={15} />
              </span>
              <span className="admin-nav__text">
                <span className="admin-nav__title">网盘管理</span>
              </span>
            </NavLink>
            <NavLink
              to="/admin/crawlers"
              className={({ isActive }) =>
                `admin-nav__link ${isActive ? "is-active" : ""}`
              }
            >
              <span className="admin-nav__icon" aria-hidden="true">
                <SpiderIcon size={15} />
              </span>
              <span className="admin-nav__text">
                <span className="admin-nav__title">爬虫管理</span>
              </span>
            </NavLink>
          </div>
          <div className="admin-nav__group">
            <span className="admin-nav__group-label">管理</span>
            <NavLink
              to="/admin/videos"
              className={({ isActive }) =>
                `admin-nav__link ${isActive ? "is-active" : ""}`
              }
            >
              <span className="admin-nav__icon" aria-hidden="true">
                <Film size={15} />
              </span>
              <span className="admin-nav__text">
                <span className="admin-nav__title">视频管理</span>
              </span>
            </NavLink>
            <NavLink
              to="/admin/tags"
              className={({ isActive }) =>
                `admin-nav__link ${isActive ? "is-active" : ""}`
              }
            >
              <span className="admin-nav__icon" aria-hidden="true">
                <Tags size={15} />
              </span>
              <span className="admin-nav__text">
                <span className="admin-nav__title">标签管理</span>
              </span>
            </NavLink>
            <NavLink
              to="/admin/users"
              className={({ isActive }) =>
                `admin-nav__link ${isActive ? "is-active" : ""}`
              }
            >
              <span className="admin-nav__icon" aria-hidden="true">
                <Users size={15} />
              </span>
              <span className="admin-nav__text">
                <span className="admin-nav__title">用户管理</span>
              </span>
            </NavLink>
          </div>
          <div className="admin-nav__group">
            <span className="admin-nav__group-label">系统</span>
            <NavLink
              to="/admin/backup"
              className={({ isActive }) =>
                `admin-nav__link ${isActive ? "is-active" : ""}`
              }
            >
              <span className="admin-nav__icon" aria-hidden="true">
                <ArchiveRestore size={15} />
              </span>
              <span className="admin-nav__text">
                <span className="admin-nav__title">备份恢复</span>
              </span>
            </NavLink>
            <NavLink
              to="/admin/logs"
              className={({ isActive }) =>
                `admin-nav__link ${isActive ? "is-active" : ""}`
              }
            >
              <span className="admin-nav__icon" aria-hidden="true">
                <ScrollText size={15} />
              </span>
              <span className="admin-nav__text">
                <span className="admin-nav__title">日志查看</span>
              </span>
            </NavLink>
            <NavLink
              to="/admin/settings"
              className={({ isActive }) =>
                `admin-nav__link ${isActive ? "is-active" : ""}`
              }
            >
              <span className="admin-nav__icon" aria-hidden="true">
                <SlidersHorizontal size={15} />
              </span>
              <span className="admin-nav__text">
                <span className="admin-nav__title">配置面板</span>
              </span>
            </NavLink>
          </div>
        </nav>
      </aside>
      <AdminGlobalActions
        checkingUpdate={checkingUpdate}
        loggingOut={loggingOut}
        onCheckUpdate={() => void handleCheckUpdate()}
        onLogout={() => void handleLogout()}
      />
      <main className="admin-main">
        <Outlet />
      </main>
      {availableUpdate && (
        <Modal
          open
          title={`发现新版本 ${availableUpdate.latestVersion}`}
          className="admin-modal--release-notes"
          onClose={() => setAvailableUpdate(null)}
          footer={
            availableUpdate.releaseUrl ? (
              <a
                className="admin-btn is-primary"
                href={availableUpdate.releaseUrl}
                target="_blank"
                rel="noreferrer"
              >
                查看发布页
              </a>
            ) : undefined
          }
        >
          <div className="admin-release-notes">
            <div className="admin-release-notes__versions">
              <span>当前版本：{availableUpdate.currentVersion}</span>
              <span>最新版本：{availableUpdate.latestVersion}</span>
            </div>
            <section className="admin-release-notes__content" aria-label="Release Note">
              <h3>Release Note</h3>
              <div>{availableUpdate.releaseNotes?.trim() || "该版本未提供 Release Note。"}</div>
            </section>
          </div>
        </Modal>
      )}
    </div>
  );
}
