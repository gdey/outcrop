// Rename the AMO-signed .xpi from its addon-GUID-hashed default
// (e.g. 8d3e3a01f1a6490992e5-0.1.2.xpi) to outcrop-<version>_firefox.xpi.
// `web-ext sign` 10.x doesn't honor `--filename`, only `web-ext build` does,
// so we rename in a post-step.
//
// Idempotent: a no-op if the file is already named correctly.

import { readdirSync, readFileSync, renameSync } from "node:fs";
import { join } from "node:path";

const pkg = JSON.parse(readFileSync("./package.json", "utf8"));
const dir = "dist-artifacts";
const desired = `outcrop-${pkg.version}_firefox.xpi`;

const candidates = readdirSync(dir).filter(
  (f) => f.endsWith(".xpi") && f !== desired,
);

if (candidates.length === 0) {
  if (readdirSync(dir).includes(desired)) {
    console.log(`${desired} already in place`);
    process.exit(0);
  }
  console.error(`no .xpi found in ${dir}/`);
  process.exit(1);
}

if (candidates.length > 1) {
  console.error(
    `multiple .xpi files in ${dir}/ — expected one: ${candidates.join(", ")}`,
  );
  process.exit(1);
}

const source = candidates[0];
renameSync(join(dir, source), join(dir, desired));
console.log(`renamed ${source} → ${desired}`);
