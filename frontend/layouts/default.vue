<template>
  <div class="flex flex-col h-screen w-screen">
    <HeaderBar />
    <div class="flex flex-1 overflow-hidden">
      <Sidebar />
      <main class="flex-1 p-4 bg-neutral flex">
        <UApp>
          <NuxtPage />
        </UApp>
      </main>
    </div>
  </div>
</template>