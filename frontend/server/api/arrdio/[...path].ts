export default defineEventHandler(async (event) => {
  const session = await requireUserSession(event);
  if (
    !["GET", "HEAD"].includes(event.method) &&
    getHeader(event, "origin") !== getRequestURL(event).origin
  ) {
    throw createError({
      statusCode: 403,
      statusMessage: "Invalid request origin",
    });
  }
  const config = useRuntimeConfig(event);
  const path = getRouterParam(event, "path") || "";
  // Only the shared application operation surface is reachable through the browser.
  if (
    !/^(requests(?:\/\d+\/(?:bump|cancel|retry))?|status|quota|accounts|catalog|bootstrap|grants(?:\/[a-f0-9]+\/revoke)?|routing|categorize|candidates|correct|photos|me)$/.test(
      path,
    )
  ) {
    throw createError({ statusCode: 404 });
  }
  const user = session.user as { sub?: string; id?: string; email?: string };
  const identity = user.sub || user.id || user.email;
  if (!identity || !config.arrdioGatewayToken)
    throw createError({
      statusCode: 503,
      statusMessage: "Application connection is not configured",
    });
  const query = getRequestURL(event).search;
  try {
    return await $fetch(`${config.arrdioBase}/${path}${query}`, {
      method: event.method as any,
      headers: {
        Authorization: `Bearer ${config.arrdioGatewayToken}`,
        "X-Arrdio-User": identity,
        ...(getHeader(event, "idempotency-key")
          ? { "Idempotency-Key": getHeader(event, "idempotency-key")! }
          : {}),
      },
      body: ["GET", "HEAD"].includes(event.method)
        ? undefined
        : await readBody(event),
    });
  } catch (error: any) {
    throw createError({
      statusCode: error.statusCode || 502,
      statusMessage: error.data?.error || "Acquisition service unavailable",
    });
  }
});
