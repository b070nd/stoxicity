package edgar

// Downloads the nightly bulk files once for the first load, never per
// request, and refreshes them on a schedule well below the rebuild time.
// Feeds entity resolution and the first back-fill.
