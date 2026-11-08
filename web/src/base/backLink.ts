import type { LocationQuery, RouteLocationNormalizedLoaded, RouteLocationRaw } from 'vue-router'

// A link from one object's page to another object's page (ticket -> run, list -> audit)
// carries `from`, so the shell's back link returns to where the user came from instead
// of the target's own list.
//   linkFrom('/audit', route, { target_type: 'example', target_id: id, target_name: name })
export function linkFrom(
  path: string,
  route: RouteLocationNormalizedLoaded,
  query: Record<string, string> = {},
): RouteLocationRaw {
  return { path, query: { ...query, from: route.fullPath } }
}

// Only in-app paths; anything else (absolute URLs, protocol-relative) is ignored.
export function readFrom(query: LocationQuery): string | undefined {
  const v = Array.isArray(query.from) ? query.from[0] : query.from
  return typeof v === 'string' && v.startsWith('/') && !v.startsWith('//') ? v : undefined
}
