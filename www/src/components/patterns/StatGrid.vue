<template>
  <div class="gi-stat-grid">
    <slot />
  </div>
</template>

<script setup>
defineOptions({ name: 'StatGrid' })
</script>

<style scoped>
.gi-stat-grid {
  display: grid;
  gap: 12px;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin-bottom: 12px;
}

@media (max-width: 768px) {
  .gi-stat-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 480px) {
  .gi-stat-grid {
    grid-template-columns: 1fr;
  }
}
</style>
