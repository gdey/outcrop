import { cp, mkdir, copyFile } from "node:fs/promises";
import { existsSync } from "node:fs";

await mkdir("dist", { recursive: true });
await mkdir("dist/popup", { recursive: true });
await mkdir("dist/options", { recursive: true });

await copyFile("manifest.json", "dist/manifest.json");
await copyFile("src/popup/popup.html", "dist/popup/popup.html");
await copyFile("src/popup/popup.css", "dist/popup/popup.css");
await copyFile("src/options/options.html", "dist/options/options.html");
await copyFile("src/options/options.css", "dist/options/options.css");

if (existsSync("src/icons")) {
  await cp("src/icons", "dist/icons", { recursive: true });
}

console.log("static files copied.");
