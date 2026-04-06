import React from "react";
import CredentialList from "../../Access_Management/Credential_List.jsx";
import UserManagement from "../../Access_Management/Users.jsx";
import SiteAssignment from "../../Sites/Site_Assignment.jsx";

export function CredentialsRoute() {
  return <CredentialList />;
}

export function UsersRoute() {
  return <UserManagement />;
}

export function SiteAssignmentRoute() {
  return <SiteAssignment />;
}
