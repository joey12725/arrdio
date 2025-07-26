# ---------- build stage ----------
FROM node:20-alpine AS builder
WORKDIR /app

### build‑time variables arrive here
ARG GOLANG_API_URL
ARG AUTHENTIK_CLIENT_ID
ARG AUTHENTIK_CLIENT_SECRET
ARG AUTH_REDIRECT_URI
ARG NUXT_OIDC_TOKEN_KEY
ARG NUXT_OIDC_SESSION_SECRET
ARG NUXT_OIDC_AUTH_SESSION_SECRET
# expose them to Node during the build *and* copy them forward
ENV GOLANG_API_URL=$GOLANG_API_URL \
    AUTHENTIK_CLIENT_ID=$AUTHENTIK_CLIENT_ID \
    AUTHENTIK_CLIENT_SECRET=$AUTHENTIK_CLIENT_SECRET \
    AUTH_REDIRECT_URI=$AUTH_REDIRECT_URI \
    NUXT_OIDC_TOKEN_KEY=$NUXT_OIDC_TOKEN_KEY \
    NUXT_OIDC_SESSION_SECRET=$NUXT_OIDC_SESSION_SECRET \
    NUXT_OIDC_AUTH_SESSION_SECRET=$NUXT_OIDC_AUTH_SESSION_SECRET

# Corepack ships with recent Node; it activates Yarn v1 by default.
RUN corepack enable

# Copy manifest *and* lock file
COPY package.json yarn.lock ./

# Install prod + dev deps reproducibly
RUN yarn install --frozen-lockfile

# Copy the rest of the source and build
COPY . .
RUN yarn build          # or `yarn run build`, whatever your script name is

# ---------- runtime stage ----------
FROM node:20-alpine
WORKDIR /app
ENV NODE_ENV=production

# Only copy the built output
COPY --from=builder /app/.output .output
EXPOSE 3000
CMD ["node", ".output/server/index.mjs"]
