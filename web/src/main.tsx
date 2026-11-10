import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { ApiError } from "./api";
import { RequireAuth } from "./auth";
import { BroadcastEdit, BroadcastList } from "./pages/Broadcasts";
import { Channels, TestInbox } from "./pages/Channels";
import { FormEdit, FormList } from "./pages/Forms";
import { FunnelEdit, FunnelList } from "./pages/Funnels";
import Contacts from "./pages/Contacts";
import Dashboard from "./pages/Dashboard";
import { JourneyEdit, JourneyList } from "./pages/Journeys";
import { ListDetail, ListIndex } from "./pages/Lists";
import { Login, Signup } from "./pages/Login";
import { DWCEdit, DWCList, FocusEdit, FocusList } from "./pages/OnSite";
import { InAppEdit, InAppList } from "./pages/InApp";
import { SegmentEdit, SegmentList } from "./pages/Segments";
import { Access, Attributes, Scoring, Team } from "./pages/Settings";
import { Roles } from "./pages/Roles";
import { AuditLog } from "./pages/Audit";
import { WebhookEdit, WebhookList } from "./pages/Webhooks";
import { Imports } from "./pages/Imports";
import { DataPrivacy } from "./pages/DataSettings";
import { TemplateEdit, TemplateList } from "./pages/Templates";
import Shell from "./Shell";
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

const router = createBrowserRouter([
  { path: "/login", element: <Login /> },
  { path: "/signup", element: <Signup /> },
  {
    path: "/",
    element: <RequireAuth><Shell /></RequireAuth>,
    children: [
      { index: true, element: <Dashboard /> },
      { path: "contacts", element: <Contacts /> },
      { path: "segments", element: <SegmentList /> },
      { path: "segments/:id", element: <SegmentEdit /> },
      { path: "lists", element: <ListIndex /> },
      { path: "lists/:id", element: <ListDetail /> },
      { path: "journeys", element: <JourneyList /> },
      { path: "journeys/:id", element: <JourneyEdit /> },
      { path: "broadcasts", element: <BroadcastList /> },
      { path: "broadcasts/:id", element: <BroadcastEdit /> },
      { path: "templates", element: <TemplateList /> },
      { path: "templates/:id", element: <TemplateEdit /> },
      { path: "focus-items", element: <FocusList /> },
      { path: "focus-items/:id", element: <FocusEdit /> },
      { path: "web-content", element: <DWCList /> },
      { path: "web-content/:id", element: <DWCEdit /> },
      { path: "in-app", element: <InAppList /> },
      { path: "in-app/:id", element: <InAppEdit /> },
      { path: "forms", element: <FormList /> },
      { path: "forms/:id", element: <FormEdit /> },
      { path: "funnels", element: <FunnelList /> },
      { path: "funnels/:id", element: <FunnelEdit /> },
      { path: "channels", element: <Channels /> },
      { path: "test-inbox", element: <TestInbox /> },
      { path: "settings/team", element: <Team /> },
      { path: "settings/access", element: <Access /> },
      { path: "settings/attributes", element: <Attributes /> },
      { path: "settings/scoring", element: <Scoring /> },
      { path: "settings/roles", element: <Roles /> },
      { path: "settings/audit", element: <AuditLog /> },
      { path: "settings/data", element: <DataPrivacy /> },
      { path: "settings/webhooks", element: <WebhookList /> },
      { path: "settings/webhooks/:id", element: <WebhookEdit /> },
      { path: "imports", element: <Imports /> },
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
