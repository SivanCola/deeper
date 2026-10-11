import { spawn } from "node:child_process";

// Keep archive paths and member names in argv, never in a shell program.
export function readLinuxArchiveMember(artifact, member, kind) {
  if (kind !== "linux-tar" && kind !== "linux-deb") return Promise.reject(new Error("unsupported Linux archive kind"));
  const commands = kind === "linux-tar"
    ? [["tar", ["-xzOf", artifact, "--", member]]]
    : [["dpkg-deb", ["--fsys-tarfile", artifact]], ["tar", ["-xOf", "-", "--", member]]];
  return readProcessPipeline(commands);
}

// Require every producer to succeed: checking only tar's status can accept a
// member from a truncated/failed dpkg-deb stream. Bound bytes without buffering
// the complete decompressed archive between the two processes.
export function readProcessPipeline(commands, { maxBytes = 512 * 1024 * 1024 } = {}) {
  if (!commands.length || !Number.isSafeInteger(maxBytes) || maxBytes < 0) {
    return Promise.reject(new Error("invalid archive pipeline or output budget"));
  }
  return new Promise((resolve, reject) => {
    const children = [];
    const chunks = [];
    const errors = [];
    let size = 0;
    let closed = 0;
    let settled = false;
    const fail = (err) => {
      if (settled) return;
      settled = true;
      for (const child of children) child.kill();
      reject(err);
    };
    try {
      for (const [program, args] of commands) {
        const child = spawn(program, args, { stdio: [children.length ? "pipe" : "ignore", "pipe", "pipe"] });
        let stderr = "";
        child.stderr.on("data", (data) => { stderr = (stderr + data.toString()).slice(-64 * 1024); });
        child.on("error", fail);
        child.on("close", (code, signal) => {
          if (code !== 0) errors.push(`${program} failed (${signal || code}): ${stderr}`);
          if (++closed !== commands.length || settled) return;
          if (errors.length) { fail(new Error(errors.join("; "))); return; }
          settled = true;
          resolve(Buffer.concat(chunks, size));
        });
        if (children.length) {
          child.stdin.on("error", fail);
          children.at(-1).stdout.pipe(child.stdin);
        }
        children.push(child);
      }
      children.at(-1).stdout.on("data", (data) => {
        size += data.length;
        if (size > maxBytes) { fail(new Error("archive member exceeds output budget")); return; }
        chunks.push(data);
      });
    } catch (err) { fail(err); }
  });
}
