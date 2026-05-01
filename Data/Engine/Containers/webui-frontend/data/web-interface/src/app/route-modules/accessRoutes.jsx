import React from "react";
import CredentialList from "../../Access_Management/Credential_List.jsx";
import DirectoryServices from "../../Access_Management/Directory_Services.jsx";
import UserManagement from "../../Access_Management/Users.jsx";
import SiteAssignment from "../../Sites/Site_Assignment.jsx";

export function CredentialsRoute() {
  return <CredentialList />;
}

export function UsersRoute() {
  return <UserManagement />;
}

export function DirectoryServicesRoute() {
  return <DirectoryServices />;
}

export function SiteAssignmentRoute() {
  return <SiteAssignment />;
}
