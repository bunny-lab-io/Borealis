import React from "react";
import ScheduledJobsList from "../../Scheduling/Scheduled_Jobs_List.jsx";
import CreateJob from "../../Scheduling/Create_Job.jsx";
import CreateOnboardingJob from "../../Scheduling/Create_Onboarding_Job.jsx";

export function JobListRoute() {
  return <ScheduledJobsList />;
}

export function JobEditorRoute() {
  return <CreateJob />;
}

export function OnboardingJobEditorRoute() {
  return <CreateOnboardingJob />;
}
