// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: '2025-05-15',
  devtools: { enabled: true },
  modules: ['@nuxt/fonts', '@nuxt/image', '@nuxt/ui', 'nuxt-auth-utils'],
  css: ['~/assets/css/main.css'],
  runtimeConfig: {
    oauth: {
      authentik: {
        clientId: process.env.NUXT_OAUTH_AUTHENTIK_CLIENT_ID || process.env.NUXT_OIDC_PROVIDERS_OIDC_CLIENT_ID,
        clientSecret: process.env.NUXT_OAUTH_AUTHENTIK_CLIENT_SECRET || process.env.NUXT_OIDC_PROVIDERS_OIDC_CLIENT_SECRET,
        domain: process.env.NUXT_OAUTH_AUTHENTIK_DOMAIN || process.env.NUXT_OIDC_PROVIDERS_OIDC_BASE_URL,
        redirectURL: process.env.NUXT_OAUTH_AUTHENTIK_REDIRECT_URL || process.env.NUXT_OIDC_PROVIDERS_OIDC_REDIRECT_URI || 'http://localhost:3000/auth/authentik'
      }
    },
    public: {
      apiBase: process.env.NUXT_PUBLIC_API_BASE || process.env.GOLANG_API_URL
    }
  },
  nitro: {
    preset: 'node-server',
  },
  ui: {
    theme: {
      colors: ['primary', 'secondary', "netural", "info", 'error', "success", "warning"]
    }},

})