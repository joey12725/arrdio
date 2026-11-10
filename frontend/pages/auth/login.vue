<script setup lang="ts">
definePageMeta({ layout: "auth" });
useHead({ title: "Sign in · ARRDIO" });
</script>
<template>
  <section class="sign-in">
    <ArrdioLogo />
    <h1>Sign in</h1>
    <a href="/api/auth/authentik">Continue with Authentik →</a>
  </section>
</template>
<style scoped>
.sign-in {
  max-width: 440px;
  text-align: center;
  padding: 40px 30px;
  background: #151e30;
  border: 1px solid #3b4c64;
  border-top: 2px solid var(--pink);
  border-radius: 20px;
}
.brand {
  font-family: "Tilt Warp", sans-serif;
  font-size: 38px;
  letter-spacing: 2px;
}
.brand span {
  color: var(--mint);
}
h1 {
  font-family: "Tilt Neon", sans-serif;
  font-size: 32px;
  font-weight: 500;
}
p {
  color: #c5d0e2;
  line-height: 1.7;
}
a {
  display: block;
  text-decoration: none;
  background: var(--mint);
  color: #081d1c;
  border-radius: 10px;
  padding: 15px;
  font-weight: 700;
  margin-top: 30px;
}
</style>
