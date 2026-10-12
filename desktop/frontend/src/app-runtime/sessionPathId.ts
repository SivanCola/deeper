/** Derive a session id from a transcript path: strip directories and extension. */
export function taskSessionIDFromPath(path: string): string {
  const base = path.replace(/\\/g, "/").split("/").pop() || "";
  const extension = base.lastIndexOf(".");
  return extension > 0 ? base.slice(0, extension) : base;
}
