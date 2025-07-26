// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: '2025-05-15',
  devtools: { enabled: true },
  modules: ['@nuxt/fonts', '@nuxt/image', '@nuxt/ui', 'nuxt-oidc-auth'],
  css: ['~/assets/css/main.css'],
  runtimeConfig: {
    public: {
      apiBase: process.env.GOLANG_API_URL
    }
  },
  vite: {
    server: {
      host: true,
      allowedHosts: ['.ngrok-free.app'],
      hmr: { protocol: 'https', host: process.env.NGROK_HOST, port: 443 }
    }
  },
  ui: {
    theme: {
      colors: ['primary', 'secondary', "netural", "info", 'error', "success", "warning"]
    }},
    oidc: {

      defaultProvider: 'oidc',
      providers: {
        oidc: {
          clientId:         process.env.AUTHENTIK_CLIENT_ID,
          clientSecret:      process.env.AUTHENTIK_CLIENT_SECRET,
          authorizationUrl: `https://auth.example.com/application/o/authorize/`,
          tokenUrl:         `https://auth.example.com/application/o/token/`,
          userInfoUrl:      `https://auth.example.com/application/o/userinfo/`,       
          openIdConfiguration: `https://auth.example.com/application/o/arrdiov2/.well-known/openid-configuration`,
          redirectUri: process.env.AUTH_REDIRECT_URI,
          scope: ['openid','profile','email','offline_access'],
          pkce: true,
        }
      },
      // turn on the global guard and opt into a custom login page
      middleware: {
        globalMiddlewareEnabled: true,
        customLoginPage: true
      },
      devMode: {
      enabled: false,
    }
  },

})