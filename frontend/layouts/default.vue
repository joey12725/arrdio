<script setup lang="ts">
const { clear } = useUserSession();
async function signOut() {
  await clear();
  await navigateTo("/auth/login");
}
</script>
<template>
  <div class="app-shell">
    <header class="app-header">
      <NuxtLink to="/" class="brand" aria-label="ARRDIO home"
        ><ArrdioLogo
      /></NuxtLink>
      <nav aria-label="Main navigation">
        <NuxtLink to="/">Queue</NuxtLink
        ><NuxtLink to="/settings">Settings</NuxtLink>
        <button class="sign-out" @click="signOut">Sign out</button>
      </nav>
    </header>
    <main id="main-content"><slot /></main>
  </div>
</template>

<style>
.sign-out {
  background: none;
  border: 0;
  padding: 0;
  color: var(--muted);
  font-size: 13px;
}
:root {
  color-scheme: dark;
  --navy: #0c1222;
  --mint: #36d1b2;
  --pink: #f00cb3;
  --header: #1e2838;
  --muted: #b8c5d7;
}
body {
  margin: 0;
  background: var(--navy);
  color: #f3f6fc;
  font-family: "Public Sans", sans-serif;
}
* {
  box-sizing: border-box;
}
button,
input,
select,
textarea {
  font: inherit;
}
button,
a,
input,
select,
textarea {
  outline-offset: 4px;
}
button {
  cursor: pointer;
}
button:disabled {
  opacity: 0.5;
  cursor: wait;
}
a {
  color: var(--mint);
}
.app-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 14px max(24px, calc((100vw - 1280px) / 2));
  background: var(--header);
  border-bottom: 1px solid #354057;
}
.brand {
  font-family: "Tilt Warp", sans-serif;
  font-size: 30px;
  color: #fff;
  text-decoration: none;
  letter-spacing: 2px;
}
.brand span {
  color: var(--mint);
}
.brand small {
  display: block;
  font:
    10px "Public Sans",
    sans-serif;
  letter-spacing: 2px;
  color: var(--muted);
  margin-top: 5px;
}
nav {
  display: flex;
  gap: 24px;
}
nav a {
  display: inline-flex;
  align-items: center;
  min-height: 44px;
  color: #d3ddeb;
  text-decoration: none;
  font-size: 14px;
}
nav .router-link-exact-active {
  color: var(--mint);
}
#main-content {
  max-width: 1280px;
  margin: auto;
  padding: 42px 24px 80px;
}
h1 {
  font-family: "Tilt Neon", sans-serif;
  font-size: clamp(32px, 5vw, 48px);
  font-weight: 500;
  line-height: 1.1;
  margin: 0 0 14px;
}
h2 {
  font-size: 22px;
  margin-top: 0;
}
.muted {
  color: var(--muted);
}
.eyebrow {
  font-size: 11px;
  letter-spacing: 2px;
  color: var(--mint);
  text-transform: uppercase;
}
.panel {
  background: #151e30;
  border: 1px solid #303e54;
  border-radius: 18px;
  padding: 24px;
}
.button {
  background: var(--mint);
  border: 0;
  border-radius: 9px;
  color: #081d1c;
  padding: 12px 20px;
  font-weight: 700;
}
.button.secondary {
  background: #253248;
  color: #eef4ff;
  border: 1px solid #45536b;
}
.button.danger {
  background: transparent;
  color: #ffb0da;
  border: 1px solid #814365;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 8px;
  font-size: 13px;
  color: #d4dcea;
}
.field input,
.field select,
.field textarea {
  width: 100%;
  background: #0d1526;
  border: 1px solid #47556d;
  border-radius: 8px;
  padding: 12px;
  color: white;
}
.notice {
  border-left: 3px solid var(--pink);
  padding: 12px 16px;
  background: #2b1c34;
  margin: 16px 0;
  color: #ffe0f4;
}
.actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}
.empty {
  text-align: center;
  padding: 64px 20px;
  color: var(--muted);
}
@media (max-width: 600px) {
  .app-header {
    padding: 10px 16px;
    flex-wrap: wrap;
    gap: 8px;
  }
  .brand {
    font-size: 24px;
  }
  nav {
    flex-direction: row;
    gap: 16px;
    width: 100%;
    justify-content: space-between;
    font-size: 13px;
  }
  #main-content {
    padding: 20px 16px max(40px, env(safe-area-inset-bottom));
  }
  .panel {
    padding: 18px;
  }
}
.sign-out {
  min-height: 44px;
  min-width: 44px;
}
.field input,
.field select,
.field textarea {
  font-size: 16px;
  min-height: 44px;
}
.button {
  min-height: 44px;
}
.brand {
  letter-spacing: normal;
}
.app-header nav a[aria-current="page"] {
  border-bottom: 2px solid var(--mint);
}
</style>
