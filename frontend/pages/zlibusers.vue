<script setup lang="ts">

    interface ZLibUser {
        email: string
        download_count: number
        id: number
    }
    const zlibFetch = useFetch<ZLibUser[]>('/api/getZLibUsers')

    const { data, refresh } = toRefs(zlibFetch)

    function onRefresh(){
        refresh.value!()
    }
    
</script>

<template>
    <section class="h-full w-full">
        <div >
            <h1 class="font-header text-primary  text-[40px] text-shadow[#36d1b2] leading-none tracking-wide ">ZLibrary Users</h1>
            
            <hr class="border-0 border-t-2 border-green-600 w-64 mx-auto mb-8" />
        </div>
    
        <div class="flex flex-row gap-1">
            <div class="flex flex-row gap-5">
                <ZLibraryUserCard v-for="user in data" 
                :key="user.id"
                :email="user.email"
                :download_count="user.download_count"
                :add_card_bool="false"
                @deleted="onRefresh"/>
            </div>
            <div>
                <ZLibraryUserCard 
                :add_card_bool="true"
                @created="onRefresh"/>
            </div>
        </div>
    </section>
</template>