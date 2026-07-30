import { Suspense, lazy, useEffect, type ReactNode } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { SkyStarfield } from "@/components/SkyStarfield";
import { AdminLayout } from "@/admin/AdminLayout";
import { RequireAuth } from "@/admin/RequireAuth";
import { RequireAdmin } from "@/admin/RequireAdmin";
import { rememberVideoReturnPath, routeToPath } from "@/lib/videoReturnPath";

const HomePage = lazy(() => import("@/pages/HomePage"));
const ListingPage = lazy(() => import("@/pages/ListingPage"));
const ShortsPage = lazy(() => import("@/pages/ShortsPage"));
const UploadPage = lazy(() => import("@/pages/UploadPage"));
const VideoDetailPage = lazy(() => import("@/pages/VideoDetailPage"));
const SharedVideoPage = lazy(() => import("@/pages/SharedVideoPage"));

const LoginPage = lazy(() =>
  import("@/admin/LoginPage").then((module) => ({ default: module.LoginPage }))
);
const DrivesPage = lazy(() =>
  import("@/admin/DrivesPage").then((module) => ({ default: module.DrivesPage }))
);
const CrawlersPage = lazy(() =>
  import("@/admin/CrawlersPage").then((module) => ({
    default: module.CrawlersPage,
  }))
);
const VideosPage = lazy(() =>
  import("@/admin/VideosPage").then((module) => ({ default: module.VideosPage }))
);
const TagsPage = lazy(() =>
  import("@/admin/TagsPage").then((module) => ({ default: module.TagsPage }))
);
const DuplicateReviewsPage = lazy(() =>
  import("@/admin/DuplicateReviewsPage").then((module) => ({
    default: module.DuplicateReviewsPage,
  }))
);
const ThemePage = lazy(() =>
  import("@/admin/ThemePage").then((module) => ({ default: module.ThemePage }))
);
const BackupPage = lazy(() =>
  import("@/admin/BackupPage").then((module) => ({ default: module.BackupPage }))
);
const UsersPage = lazy(() =>
  import("@/admin/UsersPage").then((module) => ({ default: module.UsersPage }))
);

function PageSuspense({ children }: { children: ReactNode }) {
  return <Suspense fallback={null}>{children}</Suspense>;
}

function VideoReturnPathRecorder() {
  const location = useLocation();

  useEffect(() => {
    rememberVideoReturnPath(routeToPath(location));
  }, [location.pathname, location.search, location.hash]);

  return null;
}

export default function App() {
  return (
    <>
      {/* 星空蓝主题的固定位置星星层，仅在 data-theme="sky" 下可见 */}
      <SkyStarfield />
      <VideoReturnPathRecorder />
      <Routes>
        <Route
          path="/login"
          element={
            <PageSuspense>
              <LoginPage />
            </PageSuspense>
          }
        />

        {/* 一次性分享页公开；具体视频和媒体请求由分享会话单独鉴权。 */}
        <Route
          path="/share"
          element={
            <PageSuspense>
              <SharedVideoPage />
            </PageSuspense>
          }
        />

        {/* 主站需要登录 */}
        <Route
          path="/"
          element={
            <RequireAuth>
              <PageSuspense>
                <HomePage />
              </PageSuspense>
            </RequireAuth>
          }
        />
        <Route
          path="/list"
          element={
            <RequireAuth>
              <PageSuspense>
                <ListingPage />
              </PageSuspense>
            </RequireAuth>
          }
        />
        <Route
          path="/shorts"
          element={
            <RequireAuth>
              <PageSuspense>
                <ShortsPage />
              </PageSuspense>
            </RequireAuth>
          }
        />
        <Route
          path="/upload"
          element={
            <RequireAuth>
              <RequireAdmin>
                <PageSuspense>
                  <UploadPage />
                </PageSuspense>
              </RequireAdmin>
            </RequireAuth>
          }
        />
        <Route
          path="/video/:id"
          element={
            <RequireAuth>
              <PageSuspense>
                <VideoDetailPage />
              </PageSuspense>
            </RequireAuth>
          }
        />

        {/* 管理后台也需要登录+管理员权限 */}
        <Route
          path="/admin"
          element={
            <RequireAuth>
              <RequireAdmin>
                <AdminLayout />
              </RequireAdmin>
            </RequireAuth>
          }
        >
          <Route index element={<Navigate to="/admin/drives" replace />} />
          <Route
            path="drives"
            element={
              <PageSuspense>
                <DrivesPage />
              </PageSuspense>
            }
          />
          <Route
            path="crawlers"
            element={
              <PageSuspense>
                <CrawlersPage />
              </PageSuspense>
            }
          />
          <Route
            path="videos"
            element={
              <PageSuspense>
                <VideosPage />
              </PageSuspense>
            }
          />
          <Route
            path="duplicate-reviews"
            element={
              <PageSuspense>
                <DuplicateReviewsPage />
              </PageSuspense>
            }
          />
          <Route
            path="tags"
            element={
              <PageSuspense>
                <TagsPage />
              </PageSuspense>
            }
          />
          <Route
            path="theme"
            element={
              <PageSuspense>
                <ThemePage />
              </PageSuspense>
            }
          />
          <Route
            path="backup"
            element={
              <PageSuspense>
                <BackupPage />
              </PageSuspense>
            }
          />
          <Route
            path="users"
            element={
              <PageSuspense>
                <UsersPage />
              </PageSuspense>
            }
          />
        </Route>

        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </>
  );
}
