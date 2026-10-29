(function (root, factory) {
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  root.ContextProjectOverview = api;
})(typeof globalThis === "undefined" ? this : globalThis, function () {
  const integLifeChildren = [
    { id: "visual", name: "Knowledge visual", status: "active", summary: "Interactive visual explanations and their release queue.", activity: "A new lesson is ready for review.", blocker: "", next: "Review the next interactive lesson.", tasks: [{ title: "Ship the next visual lesson", kind: "task", status: "in_progress" }] },
    { id: "knowledge-graph", name: "Knowledge graph", status: "active", summary: "Shared concepts, sources and provenance for the visual library.", activity: "Source links were refreshed.", blocker: "", next: "Validate the latest source relationships.", tasks: [{ title: "Validate source relationships", kind: "run", status: "queued" }] },
    { id: "lingulet", name: "Lingulet", status: "active", summary: "Language learning product, course content and learner feedback.", activity: "Stage 1 content is in progress.", blocker: "", next: "Resolve the next content quality review.", tasks: [{ title: "Review Stage 1 content", kind: "task", status: "blocked" }] },
    { id: "delivery", name: "Deployment & traffic", status: "monitoring", summary: "Production releases, public routes and traffic health.", activity: "Public routes are being monitored.", blocker: "", next: "Check the latest deployment and traffic signals.", tasks: [{ title: "Verify production traffic", kind: "run", status: "running" }] }
  ];

  const defaultDeployment = {
    environment: "production",
    status: "unknown",
    commit: "",
    branch: "main",
    route: "",
    checkedAt: "",
    blocker: "Deployment metadata is not connected yet."
  };

  function normalizeDeployment(value) {
    const deployment = value && typeof value === "object" ? value : {};
    return {
      environment: String(deployment.environment || defaultDeployment.environment),
      status: String(deployment.status || defaultDeployment.status),
      commit: String(deployment.commit || ""),
      branch: String(deployment.branch || defaultDeployment.branch),
      route: String(deployment.route || ""),
      checkedAt: String(deployment.checkedAt || deployment.checked_at || ""),
      blocker: String(deployment.blocker || defaultDeployment.blocker)
    };
  }

  function buildProjectOverview(project) {
    const name = String(project && project.name || "Project");
    const isIntegLife = /integ\s*\.\s*life|integ\s+life/i.test(name);
    const children = isIntegLife ? integLifeChildren : [{
      id: "context", name: "Context workspace", status: "active", summary: "Append-only records, files, state and integrations for this project.", activity: "Project workspace is ready.", blocker: "", next: "Capture the next decision or source.", tasks: [{ title: "Capture the next project update", kind: "task", status: "ready" }]
    }];
    return {
      id: project && project.id || "",
      name,
      status: "active",
      description: String(project && project.description || ""),
      children,
      deployment: normalizeDeployment(project && project.deployment)
    };
  }

  function overviewChildKey(projectID, childID) { return `${projectID || "project"}:${childID}`; }
  function filterOverviewChildren(children, status) {
    if (!status || status === "all") return children;
    return children.filter((child) => child.status === status || child.tasks.some((task) => task.status === status));
  }
  return { buildProjectOverview, overviewChildKey, filterOverviewChildren, normalizeDeployment };
});
