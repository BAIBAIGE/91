type PlayerVolumeStorage = {
  get(key: string): unknown;
  set(key: string, value: unknown): void;
  del(key: string): void;
};

export function disablePlayerVolumePersistence(storage: PlayerVolumeStorage) {
  if (storage.get("volume") !== undefined) {
    storage.del("volume");
  }

  // Native controls retain this storage object, so filter writes on the instance.
  const save = storage.set.bind(storage);
  storage.set = (key, value) => {
    if (key !== "volume") save(key, value);
  };
}
