export default defineOAuthAuthentikEventHandler({
  async onSuccess(event, { user }) {
    await setUserSession(event, { user });
    return sendRedirect(event, "/");
  },
  onError(event) {
    console.error("Authentik OAuth failed; sensitive provider details omitted");
    return sendRedirect(event, "/auth/login");
  },
});
