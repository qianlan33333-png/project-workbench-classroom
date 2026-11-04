import { cp } from "node:fs/promises";

// Keep the published CLI downloads in every frontend release.
await cp(new URL("../../cli/", import.meta.url), new URL("../dist/cli/", import.meta.url), {
  recursive: true,
});
