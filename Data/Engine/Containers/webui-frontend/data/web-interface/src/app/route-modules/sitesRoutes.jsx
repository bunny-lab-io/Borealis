import React from "react";
import { redirect } from "react-router-dom";
import SiteList, { loadSiteListPageData } from "../../Sites/Site_List";
import { APP_PATHS } from "../routes/paths.js";

export async function SitesListRouteLoader({ request }) {
  const url = new URL(request.url);
  const tab = String(url.searchParams.get("tab") || "").trim().toLowerCase();
  if (["workers", "site_workers", "active_site_workers"].includes(tab)) {
    throw redirect(APP_PATHS.siteWorkers);
  }
  return loadSiteListPageData(request);
}

export function SitesListRoute() {
  return <SiteList />;
}
