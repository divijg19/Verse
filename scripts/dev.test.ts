import { describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { localDatabaseURL, mergeEnvAt } from "./dev";

// The development environment file is the one place a hosted connection string can survive on a
// developer's machine, and the one place a stale copy of one can be mistaken for the live one.
//
// These tests cover the merge that writes it. The behaviour they lock in is that DATABASE_URL is
// replaced where it is rather than appended, and that the credentials a developer has already
// chosen are never replaced for them.

function envPath(): string {
  return join(mkdtempSync(join(tmpdir(), "verse-env-")), ".env");
}

describe("mergeEnvAt", () => {
  test("replaces the database URL where it is", () => {
    const path = envPath();
    writeFileSync(
      path,
      [
        "# A file born with a hosted database.",
        'DATABASE_URL="postgresql://user:secret@db.example.com:5432/verse?sslmode=require"',
        "VERSE_AUTHORIZATION=kept-passphrase",
        "VERSE_AUTH_SECRET=kept-secret",
        "",
      ].join("\n"),
    );

    mergeEnvAt(path);

    const contents = readFileSync(path, "utf8");
    expect(contents.split("DATABASE_URL=").length - 1).toBe(1);
    expect(contents).not.toContain("db.example.com");
    expect(contents).toContain(`DATABASE_URL=${localDatabaseURL}`);
  });

  test("collapses duplicate definitions of the variable", () => {
    const path = envPath();
    writeFileSync(
      path,
      [
        'DATABASE_URL="postgresql://user:secret@db.example.com:5432/verse?sslmode=require"',
        "VERSE_AUTHORIZATION=kept-passphrase",
        "VERSE_AUTH_SECRET=kept-secret",
        "DATABASE_URL=postgres://verse:verse@localhost:5432/verse?sslmode=disable",
        "DATABASE_URL=postgres://verse:verse@localhost:5432/verse?sslmode=disable",
        "",
      ].join("\n"),
    );

    mergeEnvAt(path);

    const contents = readFileSync(path, "utf8");
    expect(contents.split("DATABASE_URL=").length - 1).toBe(1);
    expect(contents).not.toContain("db.example.com");
  });

  test("keeps the credentials the developer chose", () => {
    const path = envPath();
    writeFileSync(
      path,
      "VERSE_AUTHORIZATION=kept-passphrase\nVERSE_AUTH_SECRET=kept-secret\n",
    );

    mergeEnvAt(path);

    const contents = readFileSync(path, "utf8");
    expect(contents).toContain("VERSE_AUTHORIZATION=kept-passphrase");
    expect(contents).toContain("VERSE_AUTH_SECRET=kept-secret");
  });

  test("is idempotent", () => {
    const path = envPath();
    writeFileSync(
      path,
      'DATABASE_URL="postgresql://user:secret@db.example.com:5432/verse?sslmode=require"\n',
    );

    mergeEnvAt(path);
    const first = readFileSync(path, "utf8");

    mergeEnvAt(path);
    const second = readFileSync(path, "utf8");

    expect(second).toBe(first);
  });

  test("creates a file when there is none", () => {
    const path = envPath();
    expect(existsSync(path)).toBe(false);

    mergeEnvAt(path);

    const contents = readFileSync(path, "utf8");
    expect(contents).toContain(`DATABASE_URL=${localDatabaseURL}`);
    expect(contents).toContain("VERSE_AUTHORIZATION=");
    expect(contents).toContain("VERSE_AUTH_SECRET=");
  });
});
