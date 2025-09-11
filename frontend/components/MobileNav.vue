<script setup lang="js">
const navItems = ref(
    [
        [
            {
            label: "Book Search",
            icon: 'fluent:book-search-24-regular',
            to: '/'
            },
        {
        label: "Settings",
        icon: 'eva:settings-2-outline',
        children: [ 
          {  label: "ZLib Users",
            icon: 'icon-park-outline:user',
            to: '/zlibusers',
        },
          {
            label: "Download Categories",
            icon: 'dashicons:category',
            to: '/category',
          }
        ]
        },
   
    ]
])


</script>
<template>
    <UDropdownMenu
    :items="navItems"
    :content="{
      align: 'start',
      side: 'bottom',
      sideOffset: 8
    }"
    :ui="{
      content: 'w-48'
    }"
  >
    <UButton icon="i-lucide-menu" color="neutral" variant="ghost" />
  </UDropdownMenu>
</template>