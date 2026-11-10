# ---------- build stage ----------
FROM node:22-alpine AS builder
WORKDIR /app
RUN npm install -g npm@11.6.2



# Corepack ships with recent Node; it activates Yarn v1 by default.

# Copy manifest *and* lock file
COPY package.json package-lock.json ./

# Install prod + dev deps reproducibly
RUN npm ci --ignore-scripts

# Copy the rest of the source and build
COPY . .
RUN npm run build

# ---------- runtime stage ----------
FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production

# Only copy the built output
COPY --from=builder /app/.output .output
USER node
EXPOSE 3000
CMD ["node", ".output/server/index.mjs"]
