import { access, mkdir, writeFile } from "node:fs/promises";

const distDir = new URL("../dist/", import.meta.url);
const indexFile = new URL("../dist/index.html", import.meta.url);

await mkdir(distDir, { recursive: true });

try {
  await access(indexFile);
} catch {
  await writeFile(
    indexFile,
    "<!doctype html><html><body><p>VaultDB Web UI placeholder.</p></body></html>",
    "utf8",
  );
}

console.log("Web UI build completed.");
