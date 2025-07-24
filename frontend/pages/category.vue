<script setup lang="ts">

    interface DownloadCategory {
        id: number
        category: string
        savepath: string
    }
    const categoryFetch = useFetch<DownloadCategory[]>('/api/getCategory')

    const { data, refresh } = toRefs(categoryFetch)

    function onRefresh(){
        refresh.value!()
    }
    
</script>

<template>
    <section class="h-full w-full">
        <div >
            <h1 class="font-header text-primary  text-[40px] text-shadow[#36d1b2] leading-none tracking-wide ">Download Categories</h1>
            
            <hr class="border-0 border-t-2 border-green-600 w-64 mx-auto mb-8" />
        </div>
    
        <div class="flex flex-row gap-1">
            <div class="flex flex-row gap-5">
                <DownloadCategoryCard v-for="downloadcategory in data" 
                :key="downloadcategory.id"
                :category="downloadcategory.category"
                :savepath="downloadcategory.savepath"
                :add_card_bool="false"
                @deleted="onRefresh"/>
            </div>
            <div>
                <DownloadCategoryCard 
                :add_card_bool="true"
                @created="onRefresh"/>
            </div>
        </div>
    </section>
</template>