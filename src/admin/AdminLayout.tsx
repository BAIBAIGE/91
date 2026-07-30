import { useEffect, useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import {
  ArchiveRestore,
  Film,
  GitCompare,
  HardDrive,
  Home,
  MoreVertical,
  Palette,
  Tags,
  Users,
} from "lucide-react";
import * as api from "./api";
import { useAuth } from "./AuthContext";
import { useToast } from "./ToastContext";
import { Modal } from "./Modal";
import { SpiderIcon } from "./icons/SpiderIcon";

export function AdminLayout() {
  const { logout } = useAuth();
  const navigate = useNavigate();
  const { show } = useToast();
  const [checkingUpdate, setCheckingUpdate] = useState(false);
  const [availableUpdate, setAvailableUpdate] = useState<api.UpdateCheck | null>(null);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);

  useEffect(() => {
    document.title = "后台管理";
  }, []);

  useEffect(() => {
    if (!mobileMenuOpen) return;
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setMobileMenuOpen(false);
      }
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [mobileMenuOpen]);

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
    try {
      await logout();
      show("已退出登录", "success");
      navigate("/login", { replace: true });
    } catch {
      show("退出失败", "error");
    }
  }

  return (
    <div className="admin-shell">
      <aside className="admin-sidebar">
        <nav className="admin-nav">
          <div className="admin-nav__group admin-nav__group--home">
            <span className="admin-nav__group-label">主站</span>
            <NavLink to="/" className="admin-nav__link">
              <span className="admin-nav__icon" aria-hidden="true">
                <Home size={15} />
              </span>
              <span className="admin-nav__text">
                <span className="admin-nav__title">返回主站</span>
              </span>
            </NavLink>
          </div>
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
              to="/admin/duplicate-reviews"
              className={({ isActive }) =>
                `admin-nav__link ${isActive ? "is-active" : ""}`
              }
            >
              <span className="admin-nav__icon" aria-hidden="true">
                <GitCompare size={15} />
              </span>
              <span className="admin-nav__text">
                <span className="admin-nav__title">重复复核</span>
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
              to="/admin/theme"
              className={({ isActive }) =>
                `admin-nav__link ${isActive ? "is-active" : ""}`
              }
            >
              <span className="admin-nav__icon" aria-hidden="true">
                <Palette size={15} />
              </span>
              <span className="admin-nav__text">
                <span className="admin-nav__title">主题外观</span>
              </span>
            </NavLink>
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
            <button
              type="button"
              className="admin-nav__link admin-nav__action"
              onClick={handleCheckUpdate}
              disabled={checkingUpdate}
            >
              <span className="admin-nav__text">
                <span className="admin-nav__title">
                  {checkingUpdate ? "检查中" : "检查更新"}
                </span>
              </span>
            </button>
            <button
              type="button"
              className="admin-nav__link admin-nav__action admin-nav__action--danger"
              onClick={handleLogout}
            >
              <span className="admin-nav__text">
                <span className="admin-nav__title">退出登录</span>
              </span>
            </button>
          </div>
        </nav>
        <button
          className="admin-sidebar__mobile-menu"
          onClick={() => setMobileMenuOpen((v) => !v)}
          aria-label="更多操作"
        >
          <MoreVertical size={18} />
        </button>
      </aside>
      {mobileMenuOpen && (
        <div className="admin-sidebar__mobile-overlay" onClick={() => setMobileMenuOpen(false)} />
      )}
      <div className={`admin-sidebar__mobile-panel${mobileMenuOpen ? " is-open" : ""}`}>
        <NavLink to="/" className="admin-sidebar__home" onClick={() => setMobileMenuOpen(false)}>
          返回主站
        </NavLink>
        <button
          className="admin-sidebar__check-update"
          onClick={() => { handleCheckUpdate(); setMobileMenuOpen(false); }}
          disabled={checkingUpdate}
        >
          {checkingUpdate ? "检查中" : "检查更新"}
        </button>
        <button className="admin-sidebar__logout" onClick={() => { handleLogout(); setMobileMenuOpen(false); }}>
          退出登录
        </button>
      </div>
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
