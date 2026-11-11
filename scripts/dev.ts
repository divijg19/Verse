// The local development servers.
//
// One file, four modes, because the four jobs share nearly all of their machinery: the same
// environment, the same ports, the same build outputs, and the same need to be interrupted
// cleanly.
//
//   bun scripts/dev.ts setup    one-time: start postgres, write the .env file, and fill the library
//   bun scripts/dev.ts author   the authoring site on http://localhost:8080
//   bun scripts/dev.ts public   the public site on http://localhost:4000
//   bun scripts/dev.ts all      both of the above, at once
//
// Three things about this shape are deliberate.
//
// The commands are Bun programs rather than shell script strings. Bun loads .env into its own
// environment, but a `bun run` script string is executed by Bun Shell, and Bun Shell does not hand
// those loaded values to the children it starts. A `go run` underneath a script string therefore
// begins with an empty DATABASE_URL, and the server refuses to start with a message that points at
// the environment rather than at the script.
//
// The public site is served from this process rather than by a second one. It is a directory of
// files, and serving it in-process means a rebuild is a function call instead of a subprocess, so
// the preview follows an edit without being restarted.
//
// And the servers refuse a database that is not on this machine. They write: the author server
// saves work, and the public build publishes it. Pointed at a hosted database, that is somebody's
// real work, and a refusal is the only thing that makes "local development never touches
// production" a property rather than a habit.

import {
  existsSync,
  readFileSync,
  statSync,
  watch,
  writeFileSync,
} from "node:fs";
import { spawnSync } from "node:child_process";
import { join, normalize, resolve } from "node:path";

const authorPort = 8080;
const publicPort = 4000;
const publicRoot = "dist";

// ---------------------------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------------------------

// localDatabaseURL is the connection string for the compose.yaml service.
//
// The credentials are the ones compose.yaml sets, and they are for a database that exists only on
// this machine. They are not a secret and are not a credential for anything reachable.
export const localDatabaseURL =
  "postgres://verse:verse@localhost:5432/verse?sslmode=disable";

// testDatabaseName is the disposable database the test suite uses.
//
// The suite empties tables, so it may not run against the database the application uses. The compose
// service creates one database and the suite needs another.
const testDatabaseName = "verse_test";

const envPath = ".env";

function commandExists(name: string): boolean {
  return spawnSync("command", ["-v", name], { shell: true }).status === 0;
}

function randomHex(bytes: number): string {
  const buffer = new Uint8Array(bytes);
  crypto.getRandomValues(buffer);
  return Array.from(buffer, (byte) => byte.toString(16).padStart(2, "0")).join(
    "",
  );
}

// randomPassphrase builds a passphrase long enough for the server to accept it.
//
// The server requires at least sixteen characters for the passphrase and thirty-two for the signing
// secret. Both are generated here rather than written down anywhere, so the developer's own
// environment carries values nobody else has seen.
function randomPassphrase(): string {
  const alphabet = "abcdefghijkmnopqrstuvwxyz23456789";
  const bytes = new Uint8Array(24);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (byte) => alphabet[byte % alphabet.length]).join("");
}

// presentKeys returns the variable names already defined in an env file.
function presentKeys(contents: string): Set<string> {
  const keys = new Set<string>();
  for (const line of contents.split("\n")) {
    const match = /^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=/.exec(line);
    const name = match?.[1];
    if (name) {
      keys.add(name);
    }
  }
  return keys;
}

// databaseHostOf names the server a connection string points at, without the credentials.
//
// The connection string carries a password, and a password does not belong in a log. The argument
// may be a bare connection string or a line from an env file.
function databaseHostOf(dsn: string): string {
  const line = /^\s*(?:export\s+)?DATABASE_URL\s*=\s*"?([^"\n]+)"?/m.exec(dsn);
  const value = line?.[1] ?? dsn;
  try {
    return new URL(value).hostname;
  } catch {
    return "";
  }
}

function startDatabase(): void {
  const runner = commandExists("podman")
    ? "podman"
    : commandExists("docker")
      ? "docker"
      : "";
  if (!runner) {
    console.log(
      "neither podman nor docker is on PATH; a local database was not started",
    );
    return;
  }

  console.log(`starting postgres with ${runner} compose`);
  const result = spawnSync(runner, ["compose", "up", "-d"], {
    stdio: "inherit",
  });
  if (result.status !== 0) {
    console.log(
      `\n${runner} compose up did not succeed; start a database yourself if you want one`,
    );
  }
}

// mergeEnv writes the local development environment.
//
// DATABASE_URL is always pointed at the local database. The file may hold a hosted one -- that is
// where it came from -- and carrying it forward would mean local development reads and writes the
// author's real work. The previous host is named when it changes, so the switch is visible rather
// than silent.
//
// The passphrase and the signing secret are generated, so nothing is written down anywhere and
// nothing has to be chosen by hand. An existing value is kept: a passphrase the developer has
// already typed into a running session is not theirs to have changed under them.
export function mergeEnv(): void {
  mergeEnvAt(envPath);
}

// mergeEnvAt writes the local development environment to a given path.
//
// The path is a parameter so that the behaviour can be tested without touching the developer's
// own file. Every setup run writes to the same place in the repository; a test that wrote there
// would be a test that edited the machine it ran on.
export function mergeEnvAt(path: string): void {
  const existing = existsSync(path) ? readFileSync(path, "utf8") : "";
  const keys = presentKeys(existing);

  const previousHost = databaseHostOf(existing);

  const missing: string[] = [];
  const additions: string[] = [];

  if (!keys.has("VERSE_AUTHORIZATION")) {
    missing.push("VERSE_AUTHORIZATION");
    additions.push(`VERSE_AUTHORIZATION=${randomPassphrase()}`);
  }
  if (!keys.has("VERSE_AUTH_SECRET")) {
    missing.push("VERSE_AUTH_SECRET");
    additions.push(`VERSE_AUTH_SECRET=${randomHex(24)}`);
  }

  // DATABASE_URL is replaced where it is, not appended.
  //
  // Appending it meant every setup run left another copy behind, and the file grew a line each
  // time. The first copy was the hosted database the file was born with, so the file kept a
  // production credential in it and the local one came last -- which works only because Bun takes
  // the last definition, a rule nothing in the file says.
  let lines = existing.split("\n");
  let replaced = false;

  lines = lines.map((line) => {
    if (!/^\s*(?:export\s+)?DATABASE_URL\s*=/.test(line)) {
      return line;
    }
    if (replaced) {
      // A second definition of the same variable is a duplicate. Dropping it leaves one value in
      // the file, and one value cannot disagree with itself.
      return "";
    }
    replaced = true;
    return `DATABASE_URL=${localDatabaseURL}`;
  });

  if (!replaced) {
    additions.unshift(`DATABASE_URL=${localDatabaseURL}`);
  }

  const header =
    existing.trim() === ""
      ? "# Local development environment. Gitignored; never commit it.\n" +
        "#\n" +
        "# Bun loads this file into the environment of `bun run` scripts, which is how\n" +
        "# `bun dev` passes these values to the server.\n"
      : "";

  const body = lines.join("\n").replace(/\n{3,}/g, "\n\n");
  const separator = body === "" || body.endsWith("\n") ? "" : "\n";
  const tail = additions.length > 0 ? header + additions.join("\n") + "\n" : "";

  writeFileSync(path, body + separator + tail);

  // Bun loaded the file into the environment before this function ran, so rewriting it changes what
  // the next run reads and nothing about what this run has. The values are assigned here so the
  // commands started from this one use the database the file now names.
  process.env.DATABASE_URL = localDatabaseURL;
  for (const addition of additions) {
    const equals = addition.indexOf("=");
    process.env[addition.slice(0, equals)] = addition.slice(equals + 1);
  }

  if (previousHost && previousHost !== databaseHostOf(localDatabaseURL)) {
    console.log(
      `${path} now points at the local database instead of ${previousHost}`,
    );
  }
  if (missing.length > 0) {
    console.log(`added ${missing.join(", ")} to ${path}`);
  }
}

// ensureTestDatabase creates the disposable database the test suite uses.
//
// The compose service creates one database and the suite needs another, because the tests empty
// tables and may not do that to the database the application uses.
function ensureTestDatabase(): void {
  if (!commandExists("psql")) {
    console.log("psql is not available, so the test database was not created");
    console.log(
      `create it with: createdb -h localhost -U verse ${testDatabaseName}`,
    );
    return;
  }

  const check = spawnSync(
    "psql",
    [
      localDatabaseURL,
      "-tAc",
      `SELECT 1 FROM pg_database WHERE datname = '${testDatabaseName}'`,
    ],
    { encoding: "utf8" },
  );
  if (check.status !== 0) {
    console.log(
      "could not reach the local database, so the test database was not created",
    );
    return;
  }
  if (check.stdout.trim() === "1") {
    return;
  }

  const create = spawnSync(
    "createdb",
    ["-h", "localhost", "-U", "verse", testDatabaseName],
    {
      encoding: "utf8",
    },
  );
  if (create.status !== 0) {
    console.log(`could not create the test database: ${create.stderr.trim()}`);
    return;
  }
  console.log(`created the ${testDatabaseName} database for the test suite`);
}

// requireLocalDatabase refuses to start against a database that is not on this machine.
//
// The dev servers write: the author server saves work, and the public build publishes it. Pointed at
// a hosted database, that is somebody's real work.
//
// A variable in the environment overrides the check, for the rare case of deliberately developing
// against a hosted database.
function requireLocalDatabase(): void {
  if (process.env.VERSE_DEV_ALLOW_REMOTE === "1") {
    console.log(
      "VERSE_DEV_ALLOW_REMOTE is set, so the database is not checked for locality",
    );
    return;
  }

  const dsn = process.env.DATABASE_URL ?? "";
  if (!dsn) {
    console.error(
      "DATABASE_URL is not set. Run `bun dev:setup` to write a local .env file.",
    );
    process.exit(1);
  }

  let host: string;
  try {
    host = new URL(dsn).hostname;
  } catch {
    console.error(`DATABASE_URL cannot be parsed: ${dsn}`);
    process.exit(1);
  }

  if (host === "localhost" || host === "::1" || host.startsWith("127.")) {
    return;
  }

  console.error(
    `refusing to start against ${host}: it is not on this machine.`,
  );
  console.error(
    "The dev servers write to the database, so local development must not touch a",
  );
  console.error(
    "hosted one. Run `bun dev:setup` to point at the local database, or set",
  );
  console.error(
    "VERSE_DEV_ALLOW_REMOTE=1 if you mean to develop against a hosted database.",
  );
  process.exit(1);
}

async function setup(): Promise<void> {
  startDatabase();
  mergeEnv();
  ensureTestDatabase();

  console.log("");
  console.log("applying the migrations");
  const migrated = await run(["go", "run", "./cmd/migrate"]);
  if (migrated !== 0) {
    console.error("the migrations did not apply; stopping");
    process.exit(migrated);
  }

  console.log("");
  console.log("filling the library with generated work");
  const seeded = await run(["go", "run", "./cmd/seed", "-reset"]);
  if (seeded !== 0) {
    console.error("the library was not filled; stopping");
    process.exit(seeded);
  }

  console.log("");
  console.log("done. start the application with:");
  console.log("");
  console.log(
    "  bun dev          the authoring site and the public site, together",
  );
  console.log("  bun dev:author   the authoring site alone");
  console.log("  bun dev:public   the public site alone");
  console.log("");
  console.log(`the work is read from ${databaseHostOf(localDatabaseURL)}`);
  console.log(`the passphrase is in ${envPath} as VERSE_AUTHORIZATION`);
}

// ---------------------------------------------------------------------------------------------
// Process helpers
// ---------------------------------------------------------------------------------------------

// run executes a command, waits for it, and returns its exit status.
//
// The child inherits this process's environment, which is where the .env values live.
async function run(command: string[]): Promise<number> {
  console.log(`\n$ ${command.join(" ")}\n`);
  const proc = Bun.spawn(command, { stdout: "inherit", stderr: "inherit" });
  return await proc.exited;
}

// pipe copies a child's output to this process's, one line at a time, with a label.
//
// Two servers writing to one terminal is unreadable without it: the lines interleave and there is
// no way to tell which produced which.
async function pipe(
  stream: ReadableStream<Uint8Array>,
  label: string,
): Promise<void> {
  for await (const chunk of stream) {
    const text = new TextDecoder().decode(chunk);
    for (const line of text.split("\n")) {
      if (line.length > 0) {
        process.stdout.write(`[${label}] ${line}\n`);
      }
    }
  }
}

// startServer starts a long-running command and returns it.
//
// The signal handlers exist because a terminal sends SIGINT to the whole foreground process group,
// so a child would otherwise be signalled twice: once by the terminal and once by this handler.
// Forwarding it explicitly means the child is asked to stop once, and this process waits for it to
// actually stop rather than leaving it running.
function startServer(
  command: string[],
  label: string,
  prefix: boolean,
): ReturnType<typeof Bun.spawn> {
  const proc = Bun.spawn(command, {
    stdout: prefix ? "pipe" : "inherit",
    stderr: prefix ? "pipe" : "inherit",
    env: { ...process.env },
  });

  if (prefix && proc.stdout && proc.stderr) {
    void pipe(proc.stdout, label);
    void pipe(proc.stderr, label);
  }

  let stopping = false;
  const forward = (signal: NodeJS.Signals) => {
    if (stopping) {
      return;
    }
    stopping = true;
    proc.kill(signal);
  };
  process.on("SIGINT", forward);
  process.on("SIGTERM", forward);

  return proc;
}

// ---------------------------------------------------------------------------------------------
// The public site
// ---------------------------------------------------------------------------------------------

// resolveRequest maps a request path to a file inside the root, or null when there is none.
//
// The path is normalized before it is joined onto the root, and the result is checked to still be
// inside the root, so a request cannot escape the served directory.
function resolveRequest(root: string, urlPath: string): string | null {
  const [beforeQuery = ""] = urlPath.split("?");
  const [withoutQuery = ""] = beforeQuery.split("#");

  let decoded: string;
  try {
    decoded = decodeURIComponent(withoutQuery);
  } catch {
    return null;
  }

  const cleaned = normalize(decoded).replace(/^(\.\.(\/|\\|$))+/, "");
  const target = join(root, cleaned);
  if (target !== root && !target.startsWith(root + "/")) {
    return null;
  }

  if (existsSync(target) && statSync(target).isDirectory()) {
    const index = join(target, "index.html");
    return existsSync(index) ? index : null;
  }

  return existsSync(target) ? target : null;
}

// servePublic serves the built site with the same contract the deployment has: a directory route
// resolves to its index.html, and an unknown path answers 404 with the built 404.html.
function servePublic(root: string, port: number): void {
  const absolute = resolve(root);
  const notFoundPage = join(absolute, "404.html");

  Bun.serve({
    port,
    async fetch(request) {
      const path = new URL(request.url).pathname;
      const file = resolveRequest(absolute, path);

      if (file) {
        return new Response(Bun.file(file));
      }
      if (existsSync(notFoundPage)) {
        return new Response(Bun.file(notFoundPage), { status: 404 });
      }
      return new Response("not found", { status: 404 });
    },
  });
}

// buildPublic regenerates the templates and republishes the site.
//
// The templates are compiled into Go, so a template edit is not visible in the output until it has
// been regenerated. The publish step then reads the published set from the database and writes the
// whole directory atomically, which is what lets the server keep serving while a build happens.
async function buildPublic(): Promise<boolean> {
  const templ = await run(["templ", "generate"]);
  if (templ !== 0) {
    return false;
  }

  // The bundles are rebuilt before the site is published, because the public one is embedded in
  // the publish binary: a TypeScript edit that did not reach it would ship the old script.
  const bundles = await run(["bun", "scripts/build_js.ts"]);
  if (bundles !== 0) {
    return false;
  }
  const publish = await run([
    "go",
    "run",
    "./cmd/publish",
    "-out",
    publicRoot,
    "-base-url",
    `http://localhost:${publicPort}`,
  ]);
  return publish === 0;
}

// The generated stylesheet.
//
// It is build output, so watching it would make the build trigger itself: the Tailwind step writes
// it, the write fires the watcher, and the watcher runs the Tailwind step again.
const generatedStylesheet = join(
  import.meta.dir,
  "..",
  "static",
  "css",
  "output.css",
);

// isWatched decides whether a changed path should trigger a rebuild.
//
// The generated Go files are excluded: the build regenerates them, and watching them would make the
// build trigger itself. The generated stylesheet is excluded for the same reason.
//
// The public stylesheet is included. It is a committed hand-written file under internal/, and it is
// the whole of the reader site's appearance: a change to it changes the output of the build, so it
// has to trigger the build. Leaving it out meant an edit to the reader's stylesheet showed nothing
// until the server was restarted.
function isWatched(filename: string | Buffer | null): boolean {
  if (typeof filename !== "string") {
    return false;
  }
  if (filename.endsWith("_templ.go")) {
    return false;
  }
  if (resolve(import.meta.dir, "..", filename) === generatedStylesheet) {
    return false;
  }
  return (
    filename.endsWith(".templ") ||
    filename.endsWith(".go") ||
    filename.endsWith(".css") ||
    filename.endsWith(".ts")
  );
}

// watchSources calls onChange when a source file changes, and returns a function that stops
// watching.
function watchSources(
  onChange: () => void,
  accepts: (filename: string) => boolean = () => true,
): () => void {
  // The static directory is watched for the authoring stylesheet's source. It is not a build
  // output directory -- the one generated file in it is excluded above -- but it does hold
  // static/css/input.css, which is where the authoring site's stylesheet starts.
  const roots = ["templ", "internal", "cmd", "web/ts", "static"].map((dir) =>
    join(import.meta.dir, "..", dir),
  );

  let timer: ReturnType<typeof setTimeout> | undefined;
  const watchers = roots
    .filter((dir) => existsSync(dir))
    .map((dir) =>
      watch(dir, { recursive: true }, (_event, filename) => {
        if (!isWatched(filename)) {
          return;
        }
        if (typeof filename === "string" && !accepts(filename)) {
          return;
        }
        if (timer) {
          clearTimeout(timer);
        }
        timer = setTimeout(onChange, 250);
      }),
    );

  return () => {
    if (timer) {
      clearTimeout(timer);
    }
    for (const watcher of watchers) {
      watcher.close();
    }
  };
}

// rebuildAssetsOnChange rebuilds the served assets whenever a source file changes.
//
// The authoring site serves its stylesheet and its two bundles from disk, so a rebuild is what
// makes an edit visible in the browser. Until v0.5.0 there was nothing to rebuild here: the
// bundles were hand-written, and editing one was picked up on the next reload.
//
// That stopped being true when the bundles became compiled output. The TypeScript sources are the
// thing an author edits, and the served file is what the browser fetches, so an edit to
// web/ts/editor.ts left static/js/editor.js stale until the script was run by hand. That is the
// whole regression, and this is the whole fix.
//
// The rebuilds are serialised for the same reason the public site's are: an edit during a build
// queues one more build rather than starting a second one alongside it.
function rebuildAssetsOnChange(): () => void {
  let building = false;
  let queued = false;

  const rebuild = async () => {
    if (building) {
      queued = true;
      return;
    }
    building = true;

    // The stylesheet first: it is what every page has, and a failure there is worth seeing
    // before the bundles are tried.
    await buildStylesheet();
    await run(["bun", "scripts/build_js.ts"]);

    building = false;
    if (queued) {
      queued = false;
      await rebuild();
    }
  };

  // Only the asset inputs. A Go file or a template changes what the server serves, and the
  // server is not restarted here -- that is a separate, pre-existing limitation -- so rebuilding
  // the assets in response to one would rebuild files that did not change.
  return watchSources(
    () => {
      void rebuild();
    },
    (filename) => filename.endsWith(".ts") || filename.endsWith(".css"),
  );
}

// rebuildOnChange rebuilds the public site whenever a source file changes.
//
// The rebuilds are serialised. An edit during a build queues one more build rather than starting a
// second one alongside it, because two publishes writing the same directory would race.
function rebuildOnChange(): () => void {
  let building = false;
  let queued = false;

  const rebuild = async () => {
    if (building) {
      queued = true;
      return;
    }
    building = true;
    await buildPublic();
    building = false;
    if (queued) {
      queued = false;
      await rebuild();
    }
  };

  return watchSources(() => {
    void rebuild();
  });
}

// ---------------------------------------------------------------------------------------------
// Modes
// ---------------------------------------------------------------------------------------------

function banner(lines: [string, string][]): void {
  const width = Math.max(...lines.map(([label]) => label.length));
  console.log("");
  for (const [label, url] of lines) {
    console.log(`  ${label.padEnd(width)}   ${url}`);
  }
  console.log("");
}

async function buildStylesheet(): Promise<number> {
  return await run([
    "bunx",
    "@tailwindcss/cli",
    "-i",
    "./static/css/input.css",
    "-o",
    "./static/css/output.css",
    "--minify",
  ]);
}

async function authorMode(): Promise<void> {
  requireLocalDatabase();

  const css = await buildStylesheet();
  if (css !== 0) {
    console.error("the stylesheet did not build; stopping");
    process.exit(css);
  }

  const proc = startServer(["go", "run", "./cmd/server"], "author", false);
  const stopWatching = rebuildAssetsOnChange();

  banner([["authoring site", `http://localhost:${authorPort}`]]);
  console.log(
    "  it rebuilds the stylesheet and the bundles when they change. ctrl-c to stop.\n",
  );

  const stop = () => {
    stopWatching();
    proc.kill("SIGTERM");
    process.exit(0);
  };
  process.on("SIGINT", stop);
  process.on("SIGTERM", stop);

  await new Promise<void>(() => undefined);
}

async function publicMode(): Promise<void> {
  requireLocalDatabase();

  if (!(await buildPublic())) {
    console.error("the public build failed; stopping");
    process.exit(1);
  }

  servePublic(publicRoot, publicPort);
  const stopWatching = rebuildOnChange();

  banner([["public site", `http://localhost:${publicPort}`]]);
  console.log("  it rebuilds when a template changes. ctrl-c to stop.\n");

  const stop = () => {
    stopWatching();
    process.exit(0);
  };
  process.on("SIGINT", stop);
  process.on("SIGTERM", stop);

  await new Promise<void>(() => undefined);
}

async function allMode(): Promise<void> {
  requireLocalDatabase();

  if (!(await buildPublic())) {
    console.error("the public build failed; stopping");
    process.exit(1);
  }
  servePublic(publicRoot, publicPort);

  const css = await buildStylesheet();
  if (css !== 0) {
    console.error("the stylesheet did not build; stopping");
    process.exit(css);
  }

  const author = startServer(["go", "run", "./cmd/server"], "author", true);
  const stopWatching = rebuildOnChange();

  banner([
    ["authoring site", `http://localhost:${authorPort}`],
    ["public site", `http://localhost:${publicPort}`],
  ]);
  console.log(
    "  the public site rebuilds when a template changes. ctrl-c to stop.\n",
  );

  const stop = () => {
    stopWatching();
    author.kill("SIGTERM");
    process.exit(0);
  };
  process.on("SIGINT", stop);
  process.on("SIGTERM", stop);

  await new Promise<void>(() => undefined);
}

// ---------------------------------------------------------------------------------------------

// The dispatch runs only when this file is the program being run.
//
// A module that starts a server when it is imported cannot be imported by a test, and the parts
// worth testing here -- the environment merge above all -- are the parts a test must be able to
// reach without starting anything.
if (import.meta.main) {
  const mode = process.argv[2] ?? "all";

  switch (mode) {
    case "setup":
      await setup();
      break;
    case "author":
      await authorMode();
      break;
    case "public":
      await publicMode();
      break;
    case "all":
      await allMode();
      break;
    default:
      console.error(`unknown mode: ${mode}`);
      console.error("usage: bun scripts/dev.ts [setup|author|public|all]");
      process.exit(2);
  }
}
