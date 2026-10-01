/** 一个可由测试手动完成的 Promise：用来让某个请求停在“进行中”，再按需释放。 */
export type Deferred<T> = { promise: Promise<T>; resolve: (value: T) => void };

export function deferred<T>(): Deferred<T> {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
