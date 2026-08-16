import { describe, expect, it } from "vitest";

import { isBackupArchiveName } from "@/lib/backupArchive";

describe("isBackupArchiveName", () => {
  it("accepts a panel backup archive name", () => {
    expect(isBackupArchiveName("backup-2026-08-16.tar.zst")).toBe(true);
    expect(isBackupArchiveName("BACKUP-2026-08-16.TAR.ZST")).toBe(true);
  });

  it("rejects other files and path separators", () => {
    expect(isBackupArchiveName("notes.txt")).toBe(false);
    expect(isBackupArchiveName("photo.png")).toBe(false);
    expect(isBackupArchiveName("panel.zst")).toBe(false);
    expect(isBackupArchiveName("dir/backup-2026-08-16.tar.zst")).toBe(false);
    expect(isBackupArchiveName("dir\\backup-2026-08-16.tar.zst")).toBe(false);
  });
});
