import type { Connect } from "vite";
import { createMockRouter } from "./router.ts";

export function mockApiPlugin(fixtureDir?: string): Connect.NextHandleFunction {
  const router = createMockRouter(fixtureDir);

  const middleware: Connect.NextHandleFunction = (req, res, next) => {
    const url = req.url ?? "";
    if (
      !url.startsWith("/api") &&
      !url.startsWith("/events") &&
      url !== "/healthz"
    ) {
      next();
      return;
    }
    router.handle(req, res, url);
  };

  return middleware;
}

export function mockApiPluginVite(fixtureDir?: string) {
  return {
    name: "canton-devkit-mock-api",
    configureServer(server: { middlewares: { use: (fn: Connect.NextHandleFunction) => void } }) {
      server.middlewares.use(mockApiPlugin(fixtureDir));
      // eslint-disable-next-line no-console
      console.log("\n  Mock API enabled — no Go backend required\n");
    },
  };
}
