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
  nitro: {
    preset: 'node-server',
    storage: { // Use local file system storage for dev quick setup
      oidc: {
        driver: 'fs',
        base: 'oidcstorage',
      },
    },
  },
  ui: {
    theme: {
      colors: ['primary', 'secondary', "netural", "info", 'error', "success", "warning"]
    }},
    oidc: {

      defaultProvider: 'oidc',
      providers: {
        oidc: {
          clientId:         '',
          clientSecret:      '',
          authorizationUrl: `https://auth.example.com/application/o/authorize/`,
          tokenUrl:         `https://auth.example.com/application/o/token/`,
          userInfoUrl:      `https://auth.example.com/application/o/userinfo/`,     
          openIdConfiguration: '',
          redirectUri: '',
          scope: ['openid','profile','email','offline_access'],
          pkce: true,

        }
      },
      // turn on the global guard and opt into a custom login page
      middleware: {
        globalMiddlewareEnabled: process.env.NODE_ENV === 'production',
        customLoginPage: true
      },
      devMode: {
        enabled: process.env.NODE_ENV !== 'production',
        userName: 'Dev User',
        userInfo: { email: 'dev@example.com', providerName: 'dev' },
        // Handy if your API expects a bearer token format
        generateAccessToken: true,
        claims: { role: 'developer' },
        issuer: 'dev-issuer',
        audience: 'dev-app',
        subject: 'dev-user'
    },
    session: {
      expirationCheck: process.env.NODE_ENV === 'production', // false in dev, true in prod
      automaticRefresh: process.env.NODE_ENV === 'production',
      expirationThreshold: 60,       // refresh 60s before exp (optional)
      maxAge: 60 * 60 * 24 * 7       // 7d session cookie (adjust to taste)
  }
  },

})