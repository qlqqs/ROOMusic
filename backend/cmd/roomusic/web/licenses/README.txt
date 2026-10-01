ROOMusic 随页面分发的字体及其许可

字体由 Fontsource 的可变字体包提供（只含 woff2，wght 轴），构建时随页面一起放在 /assets/ 下，
浏览器从同一来源加载，不访问第三方字体服务。三套字体均采用 SIL Open Font License 1.1，
许可全文见同目录下对应的 *-OFL.txt（内容与各包内的 LICENSE 一致）。

  字体                来源包                                          版本   许可文本
  Cormorant Garamond  @fontsource-variable/cormorant-garamond          5.3.0  cormorant-garamond-OFL.txt
  Lora                @fontsource-variable/lora                        5.3.0  lora-OFL.txt
  Noto Serif SC       @fontsource-variable/noto-serif-sc               5.3.0  noto-serif-sc-OFL.txt

分发约定：
- 按原样再分发包内的字体文件，不修改、不重命名；Lora 声明了保留字体名 "Lora"。
- 升级字体包时，同步更新本文件的版本与各许可文本。
