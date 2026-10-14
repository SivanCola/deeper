import type { Item } from "./useController";

export type McpListAttribution = {
  sharedHost: number;
  diskCache: number;
  remote: number;
  networkCalls: number;
};

export function mcpListAttribution(items: Item[]): McpListAttribution {
  const counts: McpListAttribution = { sharedHost: 0, diskCache: 0, remote: 0, networkCalls: 0 };
  for (const item of items) {
    if (item.kind !== "notice") continue;
    if (item.code !== "mcp_tools_list") continue;
    try {
      const detail = JSON.parse(item.detail ?? "") as Record<string, unknown>;
      if (detail.source === "shared_host") counts.sharedHost += 1;
      else if (detail.source === "disk_cache") counts.diskCache += 1;
      else if (detail.source === "remote") counts.remote += 1;
      if (detail.network_call === true) counts.networkCalls += 1;
    } catch {
      // Malformed detail cannot establish attribution.
    }
  }
  return counts;
}
