export function isBackupArchiveName(name: string): boolean {
  if (!name || name.includes("/") || name.includes("\\")) {
    return false;
  }
  return name.toLowerCase().endsWith(".tar.zst");
}
