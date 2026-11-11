// The TypeScript build.
//
// Three bundles come out of this: the editor's Focus Mode, the navigation, and the public site's
// archive search. Bun is the compiler, because Bun is already a build dependency of this repository
// for the authoring stylesheet, and a second toolchain for a second language would cost more than the
// type-checking is worth.
//
// THE OUTPUT IS COMMITTED
//
// The compiled files are in the repository, and this script is how they are regenerated. The reason is
// the same reason the generated templ files are committed: `go build` must not depend on a
// code-generation step having run.
//
// The public bundle is the clearest case. It is embedded into the publish binary with go:embed, so
// it has to exist at compile time or `go build` fails on a fresh checkout. The authoring bundles
// have the same property in a softer form: the static allowlist test asserts that
// /static/js/editor.js is served, and a file that is only produced by a build step is not there on a
// fresh checkout.
//
// The alternative -- building at deploy time and gitignoring the output -- was considered and
// rejected. It makes `go build ./...` fail on a clean clone, which is a worse thing to hand a
// contributor than a duplicated file is.
//
// THE OUTPUT IS FORMATTED AFTER IT IS BUILT
//
// `bun fmt --check` runs over the whole repository in CI, and it does not honour a biome.json
// exclusion for generated files. So the bundler's output is formatted before it is committed, and
// the check mode compares against the formatted form. Without this, every build would produce a
// file the formatter wants to change, and the currency check could never pass.
//
// Bun does not downlevel. A `??` written in a source file is a `??` in the output whatever the
// tsconfig says, which was measured rather than assumed. The transpile floor is therefore a
// discipline rather than a setting, and the tsconfig's "lib" is what holds it: an ES2022 library
// call is a type error.

import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve } from "node:path";

/** One bundle: where its source is and where its output goes. */
interface Bundle {
  /** The TypeScript entry point. */
  source: string;

  /** Where the compiled bundle is written. */
  output: string;
}

/**
 * The bundles, and the only place the mapping is written down.
 *
 * The authoring bundles keep their names, so the URLs the templates already reference do not
 * change and the static allowlist does not have to learn a new entry.
 *
 * The public bundle is embedded by the publish command and content-addressed at build time, the
 * same way the public stylesheet is. It lives beside that stylesheet because both are the public
 * site's assets, and both are inputs to the publish binary rather than files a browser requests.
 */
const bundles: Bundle[] = [
  { source: "web/ts/editor.ts", output: "static/js/editor.js" },
  { source: "web/ts/navigation.ts", output: "static/js/navigation.js" },
  {
    source: "web/ts/public_search.ts",
    output: "internal/publish/assets/public.js",
  },
];

/** The flags every bundle is built with. */
const buildFlags = ["--target=browser", "--format=iife"];

/**
 * Builds one bundle and formats the result.
 *
 * The formatting is not cosmetic: `bun fmt --check` covers the whole tree and would otherwise
 * rewrite every compiled file, so an unformatted build could never be committed and the currency
 * check could never pass.
 */
async function buildBundle(bundle: Bundle, destination: string): Promise<void> {
  mkdirSync(dirname(destination), { recursive: true });

  const build = Bun.spawn(
    ["bun", "build", bundle.source, ...buildFlags, "--outfile", destination],
    {
      cwd: projectRoot(),
      stdout: "pipe",
      stderr: "pipe",
    },
  );
  const buildErr = await new Response(build.stderr).text();
  if ((await build.exited) !== 0) {
    throw new Error(`bun build ${bundle.source} failed: ${buildErr.trim()}`);
  }

  const format = Bun.spawn(["bun", "fmt", destination], {
    cwd: projectRoot(),
    stdout: "pipe",
    stderr: "pipe",
  });
  const formatErr = await new Response(format.stderr).text();
  if ((await format.exited) !== 0) {
    throw new Error(`bun fmt ${destination} failed: ${formatErr.trim()}`);
  }
}

/** The repository root, so the script works from any directory. */
function projectRoot(): string {
  return resolve(import.meta.dir, "..");
}

/**
 * Reports whether a compiled bundle is current.
 *
 * The bundle is built into a temporary directory and compared with the committed file, byte for
 * byte. Anything else -- a different comparison, a fuzzy match -- would let a stale bundle through,
 * and a stale bundle is the whole failure this check exists to catch.
 */
async function isCurrent(bundle: Bundle): Promise<boolean> {
  const committed = resolve(projectRoot(), bundle.output);
  if (!existsSync(committed)) {
    return false;
  }

  const scratch = mkdtempSync(join(tmpdir(), "verse-js-"));
  try {
    const built = join(scratch, "bundle.js");
    await buildBundle(bundle, built);
    return readFileSync(built).equals(readFileSync(committed));
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

async function main(): Promise<void> {
  const check = process.argv.includes("--check");

  if (check) {
    const stale: string[] = [];
    for (const bundle of bundles) {
      if (!(await isCurrent(bundle))) {
        stale.push(bundle.output);
      }
    }

    if (stale.length > 0) {
      console.error(
        "these compiled bundles are not current with their sources:\n  " +
          stale.join("\n  ") +
          "\nRun `bun scripts/build_js.ts` and commit the result.",
      );
      process.exit(1);
    }

    console.log(`${bundles.length} compiled bundles are current.`);
    return;
  }

  for (const bundle of bundles) {
    const destination = resolve(projectRoot(), bundle.output);
    await buildBundle(bundle, destination);
    console.log(`built ${relative(projectRoot(), destination)}`);
  }
}

await main();
