// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: "2026-07-28",
  srcDir: ".",
  devtools: { enabled: false },
  modules: ["@nuxt/fonts", "nuxt-auth-utils"],
  css: ["~/assets/css/main.css"],
  runtimeConfig: {
    arrdioBase: "http://127.0.0.1:2131",
    arrdioGatewayToken: "",
    oauth: {
      authentik: {
        clientId:
          process.env.NUXT_OAUTH_AUTHENTIK_CLIENT_ID ||
          process.env.NUXT_OIDC_PROVIDERS_OIDC_CLIENT_ID,
        clientSecret:
          process.env.NUXT_OAUTH_AUTHENTIK_CLIENT_SECRET ||
          process.env.NUXT_OIDC_PROVIDERS_OIDC_CLIENT_SECRET,
        domain:
          process.env.NUXT_OAUTH_AUTHENTIK_DOMAIN ||
          process.env.NUXT_OIDC_PROVIDERS_OIDC_BASE_URL,
        redirectURL:
          process.env.NUXT_OAUTH_AUTHENTIK_REDIRECT_URL ||
          process.env.NUXT_OIDC_PROVIDERS_OIDC_REDIRECT_URI ||
          "http://localhost:3000/api/auth/authentik",
      },
    },
    public: {},
  },
});
