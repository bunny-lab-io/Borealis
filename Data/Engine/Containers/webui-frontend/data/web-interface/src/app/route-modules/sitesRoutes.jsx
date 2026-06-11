import React from "react";
import SiteList, { loadSiteListPageData } from "../../Sites/Site_List";

export async function SitesListRouteLoader({ request }) {
  return loadSiteListPageData(request);
}

export function SitesListRoute() {
  return <SiteList />;
}
