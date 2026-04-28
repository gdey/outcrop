import { build, context } from "esbuild";

const watch = process.argv.includes("--watch");

const common = {
  bundle: true,
  sourcemap: watch,
  minify: !watch,
  platform: "browser",
  target: ["chrome120"],
  logLevel: "info",
};

const entries = [
  { in: "src/background.ts",      out: "dist/background.js",      format: "esm"  },
  { in: "src/content.ts",         out: "dist/content.js",         format: "iife" },
  { in: "src/popup/popup.ts",     out: "dist/popup/popup.js",     format: "esm"  },
  { in: "src/options/options.ts", out: "dist/options/options.js", format: "esm"  },
];

if (watch) {
  for (const e of entries) {
    const ctx = await context({
      entryPoints: [e.in],
      outfile: e.out,
      format: e.format,
      ...common,
    });
    await ctx.watch();
  }
  console.log("watching…");
} else {
  await Promise.all(
    entries.map((e) =>
      build({
        entryPoints: [e.in],
        outfile: e.out,
        format: e.format,
        ...common,
      }),
    ),
  );
}
