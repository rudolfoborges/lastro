import { defineRailway, github, postgres, project, service } from "railway/iac";

export default defineRailway(() => {
  const db = postgres("postgres");

  const api = service("api", {
    source: github("rudolfoborges/lastro", {
      branch: "main",
      rootDirectory: "/apps/api",
      checkSuites: true,
    }),
    build: {
      builder: "DOCKERFILE",
      watchPatterns: ["/apps/api/**"],
    },
    preDeploy: "lastro migrate up",
    healthcheck: "/readyz",
    healthcheckTimeout: 60,
    deploy: {
      restartPolicyType: "ON_FAILURE",
      restartPolicyMaxRetries: 5,
    },
    env: {
      DATABASE_URL: db.env.DATABASE_URL,
      LASTRO_ENV: "production",
      LOG_LEVEL: "info",
      TRUST_PROXY_HEADERS: "true",
    },
  });

  return project("lastro", { resources: [db, api] });
});
