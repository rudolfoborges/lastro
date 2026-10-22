import { defineRailway, github, postgres, project, service } from "railway/iac";

export default defineRailway(() => {
  const db = postgres("postgres");

  const api = service("api", {
    source: github("rudolfoborges/lastro", { rootDirectory: "/apps/api" }),
    build: { watchPatterns: ["/apps/api/**"] },
    preDeploy: "lastro migrate up",
    healthcheck: "/readyz",
    healthcheckTimeout: 60,
    domains: ["lastro.code04.dev"],
    deploy: { restartPolicyMaxRetries: 5 },
    env: {
      DATABASE_URL: db.env.DATABASE_URL,
      LASTRO_ENV: "production",
      LOG_LEVEL: "info",
      TRUST_PROXY_HEADERS: "true",
      PUBLIC_BASE_URL: "https://lastro.code04.dev",
    },
  });

  return project("lastro", { resources: [db, api] });
});
