// Exact equality avoids consuming a different queued item whose text merely
// contains the accepted steer (#6238).
export function guidanceTextMatches(queued: string, consumed: string): boolean {
  const left = queued.trim();
  const right = consumed.trim();
  return Boolean(left && right && left === right);
}
