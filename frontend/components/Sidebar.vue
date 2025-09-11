<script setup>


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
  <!-- full-height dark sidebar -->
  <aside class="h-full bg-header-800 text-white flex flex-col ">
    <!-- logo at top -->
    <div class="p-4 flex-shrink-0">

    </div>

    <!-- nav menu fills remaining space -->
    <div class="flex-1 overflow-y-auto px-2">
      <UNavigationMenu
        :items="navItems"
        orientation="vertical"
        variant="pill"
        highlight
        highlight-color="green"
        class="bg-transparent" 
        

        
      />
    </div>
  </aside>
</template>