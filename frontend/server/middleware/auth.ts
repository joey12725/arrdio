export default defineEventHandler(async (event) => {
  const path = getRequestURL(event).pathname.toLowerCase();
  // nuxt-auth-utils owns session introspection and logout. It authenticates its
  // own cookie; requiring a session here breaks both login and anonymous loads.
  if (path === "/api/_auth/session") return;
  if (!path.startsWith("/api/") || path.startsWith("/api/auth/")) return;
  await requireUserSession(event);
  if (!path.startsWith("/api/arrdio/")) {
    throw createError({
      statusCode: 410,
      statusMessage: "Use the durable acquisition API",
    });
  }
  if (!["GET", "HEAD"].includes(event.method)) {
    const origin = getHeader(event, "origin");
    const url = getRequestURL(event);
    if (!origin || origin !== url.origin) {
      throw createError({
        statusCode: 403,
        statusMessage: "Invalid request origin",
      });
    }
  }
});
