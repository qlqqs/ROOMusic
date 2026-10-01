// auth 的公开入口：其他 feature 只经这里使用身份相关的合同，不引用 auth 内部文件。
export { userRoles, type UserRole, type ViewerDTO } from "./api/decoders";
