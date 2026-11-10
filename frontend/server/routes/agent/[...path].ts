// Public transport surface; the backend authenticates grants, pairing tokens,
// and resource binding. No browser gateway credential is attached here.
export default defineEventHandler(async (event) => {
  const config = useRuntimeConfig(event);
  const path = getRouterParam(event, "path") || "";
  if (path.includes("..") || path.includes("\\"))
    throw createError({ statusCode: 400 });
  const headers = getProxyRequestHeaders(event);
  delete headers["x-arrdio-user"];
  return proxyRequest(
    event,
    `${config.arrdioBase}/${path}${getRequestURL(event).search}`,
    { headers },
  );
});
