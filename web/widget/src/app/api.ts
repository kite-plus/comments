// Calls from the iframe to its own origin. Anonymous reads go without
// credentials; widget calls send the site's partitioned cookie and, when the
// browser refused it, the token from storage (login.md section 5).

export interface Author {
  kind: "user" | "guest" | "external" | "former_user";
  id?: string;
  name: string;
  url?: string;
  avatar: string;
  badge?: "owner" | "moderator";
}

export interface Comment {
  id: string;
  root_id: string | null;
  parent_id: string | null;
  reply_to?: { id: string; name: string } | null;
  author?: Author;
  body_html?: string;
  status?: string;
  pinned?: boolean;
  placeholder?: "deleted" | "hidden";
  created_at: string;
  edited_at?: string | null;
  reply_count?: number;
  replies?: Comment[];
  replies_cursor?: string | null;
}

export interface Page {
  data: Comment[];
  next_cursor: string | null;
  count: number;
  locked: boolean;
  closed: boolean;
  partial?: boolean;
  thread_id?: string;
}

export interface Viewer {
  author_id: string;
  name: string;
  avatar: string;
  role: "" | "owner" | "moderator";
  notify_replies: boolean;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

let token: string | null = null;

export const storage = {
  get(key: string): string | null {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  set(key: string, value: string): boolean {
    try {
      localStorage.setItem(key, value);
      return true;
    } catch {
      return false;
    }
  },
  del(key: string) {
    try {
      localStorage.removeItem(key);
    } catch {
      /* storage may be off */
    }
  },
};

export function setToken(t: string | null) {
  token = t;
}

async function parse<T>(r: Response): Promise<T> {
  const body = r.status === 204 ? {} : await r.json().catch(() => ({}));
  if (!r.ok) {
    const e = (body as { error?: { code: string; message: string } }).error;
    throw new ApiError(r.status, e?.code ?? "internal", e?.message ?? r.statusText);
  }
  return body as T;
}

/** An anonymous read: no cookie, no token. */
export async function read<T>(path: string): Promise<T> {
  return parse<T>(await fetch(path, { credentials: "omit" }));
}

/** A widget call with the site session; useToken false sends the cookie only. */
export async function call<T>(method: string, path: string, body?: unknown, useToken = true): Promise<{ status: number; body: T }> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (useToken && token) headers.Authorization = "Bearer " + token;
  const r = await fetch(path, { method, credentials: "same-origin", headers, body: body === undefined ? undefined : JSON.stringify(body) });
  return { status: r.status, body: await parse<T>(r) };
}

export const q = (params: Record<string, string | null | undefined>) => {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) u.set(k, v);
  return u.toString();
};
