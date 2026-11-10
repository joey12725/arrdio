<template>
  <span class="arrdio-logo"
    ><img src="/arrdiologo.svg" alt="" width="56" height="56" /><span
      >Arrdio</span
    ></span
  >
</template>
<style scoped>
.arrdio-logo > span {
  color: var(--pink);
}
.arrdio-logo {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  color: var(--pink);
  font-family: "Tilt Neon", sans-serif;
  font-size: 40px;
  line-height: 1;
  text-shadow: 0 0 9px #f00cb340;
}
.arrdio-logo img {
  display: block;
  flex-shrink: 0;
}
@media (max-width: 600px) {
  .arrdio-logo {
    font-size: 32px;
    gap: 6px;
  }
  .arrdio-logo img {
    width: 44px;
    height: 44px;
  }
}
</style>
