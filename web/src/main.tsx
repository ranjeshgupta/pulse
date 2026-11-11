import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, Navigate, RouterProvider, useParams } from "react-router-dom";
import { ApiError } from "./api";
import { RequireAuth } from "./auth";
import { BroadcastEdit, BroadcastList } from "./pages/Broadcasts";
import { Channels, TestInbox } from "./pages/Channels";
import { FormEdit, FormList } from "./pages/Forms";
import { FunnelEdit, FunnelList } from "./pages/Funnels";
import { CohortEdit, CohortList } from "./pages/Cohorts";
import { Revenue } from "./pages/Revenue";
import { ReportEdit, ReportList } from "./pages/Reports";
import Contacts from "./pages/Contacts";
import { CompanyDetail, CompanyList } from "./pages/Companies";
import { Duplicates } from "./pages/Duplicates";
import { Stages } from "./pages/Stages";
import Dashboard from "./pages/Dashboard";
import { JourneyEdit, JourneyList } from "./pages/Journeys";
import { Login, Signup } from "./pages/Login";
import { DWCEdit, FocusEdit } from "./pages/OnSite";
import { InAppEdit } from "./pages/InApp";
import { OnSiteList } from "./pages/OnSiteHub";
import { SegmentEdit, SegmentList } from "./pages/Segments";
import { EventDetail, EventList } from "./pages/Events";
import { Access, Attributes, Scoring, Team } from "./pages/Settings";
import { Roles } from "./pages/Roles";
import { AuditLog } from "./pages/Audit";
import { WebhookEdit, WebhookList } from "./pages/Webhooks";
import { IntegrationEdit, IntegrationList } from "./pages/Integrations";
import { ImportPage } from "./pages/Imports";
import { DataPrivacy } from "./pages/DataSettings";
import { TemplateEdit, TemplateList } from "./pages/Templates";
import { AssetList } from "./pages/Assets";
import { LandingPageEdit, LandingPageList } from "./pages/LandingPages";
import Shell from "./Shell";
import { SettingsHome, SettingsLayout } from "./SettingsLayout";
import "./styles.css";
import { ToastProvider } from "./ui";

// Only server and network failures are worth retrying; a 404 or 403 shows its message right away.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: (n, err) => !(err instanceof ApiError && err.status < 500) && n < 2,
    },
  },
});

/** Moved sends an old address to its new one (bookmarks and links from before the navigation review). */
function Moved({ to }: { to: (id?: string) => string }) {
  const { id } = useParams();
  return <Navigate to={to(id)} replace />;
}

const router = createBrowserRouter([
  { path: "/login", element: <Login /> },
  { path: "/signup", element: <Signup /> },
  {
    path: "/",
    element: <RequireAuth><Shell /></RequireAuth>,
    children: [
      { index: true, element: <Dashboard /> },
      { path: "contacts", element: <Contacts /> },
      { path: "contacts/duplicates", element: <Duplicates /> },
      { path: "contacts/import", element: <ImportPage /> },
      { path: "companies", element: <CompanyList /> },
      { path: "companies/:id", element: <CompanyDetail /> },
      { path: "segments", element: <SegmentList /> },
      { path: "segments/:id", element: <SegmentEdit /> },
      { path: "events", element: <EventList /> },
      { path: "events/:name", element: <EventDetail /> },
      { path: "journeys", element: <JourneyList /> },
      { path: "journeys/:id", element: <JourneyEdit /> },
      { path: "broadcasts", element: <BroadcastList /> },
      { path: "broadcasts/:id", element: <BroadcastEdit /> },
      { path: "on-site", element: <OnSiteList /> },
      { path: "on-site/focus/:id", element: <FocusEdit /> },
      { path: "on-site/content/:id", element: <DWCEdit /> },
      { path: "on-site/in-app/:id", element: <InAppEdit /> },
      { path: "templates", element: <TemplateList /> },
      { path: "templates/:id", element: <TemplateEdit /> },
      { path: "landing-pages", element: <LandingPageList /> },
      { path: "landing-pages/:id", element: <LandingPageEdit /> },
      { path: "forms", element: <FormList /> },
      { path: "forms/:id", element: <FormEdit /> },
      { path: "assets", element: <AssetList /> },
      { path: "reports", element: <ReportList /> },
      { path: "reports/:id", element: <ReportEdit /> },
      { path: "funnels", element: <FunnelList /> },
      { path: "funnels/:id", element: <FunnelEdit /> },
      { path: "cohorts", element: <CohortList /> },
      { path: "cohorts/:id", element: <CohortEdit /> },
      { path: "revenue", element: <Revenue /> },
      {
        path: "settings",
        element: <SettingsLayout />,
        children: [
          { index: true, element: <SettingsHome /> },
          { path: "team", element: <Team /> },
          { path: "roles", element: <Roles /> },
          { path: "access", element: <Access /> },
          { path: "audit", element: <AuditLog /> },
          { path: "attributes", element: <Attributes /> },
          { path: "scoring", element: <Scoring /> },
          { path: "stages", element: <Stages /> },
          { path: "data", element: <DataPrivacy /> },
          { path: "channels", element: <Channels /> },
          { path: "test-inbox", element: <TestInbox /> },
          { path: "integrations", element: <IntegrationList /> },
          { path: "integrations/:id", element: <IntegrationEdit /> },
          { path: "webhooks", element: <WebhookList /> },
          { path: "webhooks/:id", element: <WebhookEdit /> },
        ],
      },
      // Addresses from before batch 9's navigation review.
      { path: "lists", element: <Navigate to="/segments?type=static" replace /> },
      { path: "lists/:id", element: <Moved to={(id) => `/segments/${id}`} /> },
      { path: "imports", element: <Navigate to="/contacts/import" replace /> },
      { path: "focus-items", element: <Navigate to="/on-site?channel=website&type=focus" replace /> },
      { path: "focus-items/:id", element: <Moved to={(id) => `/on-site/focus/${id}`} /> },
      { path: "web-content", element: <Navigate to="/on-site?channel=website&type=content" replace /> },
      { path: "web-content/:id", element: <Moved to={(id) => `/on-site/content/${id}`} /> },
      { path: "in-app", element: <Navigate to="/on-site?channel=app" replace /> },
      { path: "in-app/:id", element: <Moved to={(id) => `/on-site/in-app/${id}`} /> },
      { path: "channels", element: <Navigate to="/settings/channels" replace /> },
      { path: "test-inbox", element: <Navigate to="/settings/test-inbox" replace /> },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>
  </StrictMode>,
);
