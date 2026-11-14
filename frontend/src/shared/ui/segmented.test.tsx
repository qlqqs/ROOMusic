import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Segmented } from "./segmented";
import { SettingRow, SettingsGroup } from "./settings-group";
import styles from "./settings-group.module.css";

// jsdom 没有 ResizeObserver 也没有布局：替换成可以手动触发的版本，卡片宽度由 getBoundingClientRect 给出。
let width = 800;
const observers = new Set<() => void>();
class TestResizeObserver {
  private readonly notify: () => void;
  constructor(callback: () => void) {
    this.notify = callback;
  }
  observe() {
    observers.add(this.notify);
  }
  disconnect() {
    observers.delete(this.notify);
  }
  unobserve() {}
}

function resize(next: number) {
  width = next;
  act(() => observers.forEach((notify) => notify()));
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  observers.clear();
  width = 800;
});

const options = [
  { value: "off", label: "关" },
  { value: "track", label: "按曲目" },
  { value: "album", label: "按专辑" },
] as const;

function Leveling({ onChange }: { onChange: (value: string) => void }) {
  const [value, setValue] = useState<string>("off");
  return (
    <SettingsGroup title="播放">
      <SettingRow label="音量均衡" description="让曲目之间响度接近">
        {(ids) => (
          <Segmented<string>
            {...ids}
            options={options}
            value={value}
            onChange={(next) => {
              setValue(next);
              onChange(next);
            }}
          />
        )}
      </SettingRow>
    </SettingsGroup>
  );
}

function measured() {
  vi.stubGlobal("ResizeObserver", TestResizeObserver);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => ({ width, height: 0, top: 0, left: 0, right: width, bottom: 0, x: 0, y: 0, toJSON: () => ({}) }));
}

describe("分段选择", () => {
  it("宽卡片里是单选组：名称与说明来自设置行，选中即提交", async () => {
    measured();
    const onChange = vi.fn();
    render(<Leveling onChange={onChange} />);
    const group = screen.getByRole("radiogroup", { name: "音量均衡" });
    expect(group.getAttribute("aria-describedby")).toBe(screen.getByText("让曲目之间响度接近").id);
    expect(screen.queryByRole("combobox")).toBeNull();
    expect((screen.getByRole("radio", { name: "关" }) as HTMLInputElement).checked).toBe(true);
    await userEvent.click(screen.getByRole("radio", { name: "按专辑" }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith("album");
    expect((screen.getByRole("radio", { name: "按专辑" }) as HTMLInputElement).checked).toBe(true);
  });

  it("卡片窄于 640px 时改用原生下拉：同样的选项、取值与名称，只渲染一种", async () => {
    measured();
    width = 400;
    const onChange = vi.fn();
    render(<Leveling onChange={onChange} />);
    expect(screen.queryByRole("radiogroup")).toBeNull();
    const select = screen.getByRole("combobox", { name: "音量均衡" }) as HTMLSelectElement;
    expect([...select.options].map((option) => option.textContent)).toEqual(["关", "按曲目", "按专辑"]);
    expect(select.selectedOptions[0]?.textContent).toBe("关");
    expect(select.getAttribute("aria-describedby")).toBe(screen.getByText("让曲目之间响度接近").id);
    await userEvent.selectOptions(select, "按曲目");
    expect(onChange).toHaveBeenCalledExactlyOnceWith("track");
    expect((screen.getByRole("combobox", { name: "音量均衡" }) as HTMLSelectElement).selectedOptions[0]?.textContent).toBe("按曲目");
  });

  it("卡片宽度变化时在两种之间切换，保留当前取值", async () => {
    measured();
    render(<Leveling onChange={vi.fn()} />);
    await userEvent.click(screen.getByRole("radio", { name: "按曲目" }));
    resize(639);
    expect(screen.queryByRole("radiogroup")).toBeNull();
    expect((screen.getByRole("combobox", { name: "音量均衡" }) as HTMLSelectElement).selectedOptions[0]?.textContent).toBe("按曲目");
    resize(640);
    expect(screen.queryByRole("combobox")).toBeNull();
    expect((screen.getByRole("radio", { name: "按曲目" }) as HTMLInputElement).checked).toBe(true);
  });

  it("没有 ResizeObserver（量不到宽度）时按宽处理；不在设置卡片里也是单选组", () => {
    vi.stubGlobal("ResizeObserver", undefined);
    render(<Leveling onChange={vi.fn()} />);
    expect(screen.getByRole("radiogroup", { name: "音量均衡" })).toBeTruthy();
  });

  it("禁用时单选组标记 aria-disabled，各项不可选", () => {
    render(
      <>
        <span id="name">串流音质</span>
        <Segmented labelId="name" options={options} value="off" disabled onChange={vi.fn()} />
      </>,
    );
    expect(screen.getByRole("radiogroup", { name: "串流音质" }).getAttribute("aria-disabled")).toBe("true");
    for (const radio of screen.getAllByRole("radio")) expect((radio as HTMLInputElement).disabled).toBe(true);
  });
});

describe("设置行", () => {
  it("名称是指向控件的 label，说明经 aria-describedby 关联；nested 是从属项的外观", () => {
    render(
      <SettingsGroup title="播放">
        <SettingRow label="交叉渐变" description="上一首淡出时下一首淡入">
          {(ids) => <input type="checkbox" role="switch" id={ids.controlId} aria-describedby={ids.descriptionId} />}
        </SettingRow>
        <SettingRow label="渐变时长" nested>
          {(ids) => <select id={ids.controlId} />}
        </SettingRow>
      </SettingsGroup>,
    );
    expect(screen.getByRole("region", { name: "播放" })).toBeTruthy();
    const toggle = screen.getByRole("switch", { name: "交叉渐变" });
    expect(toggle.getAttribute("aria-describedby")).toBe(screen.getByText("上一首淡出时下一首淡入").id);
    const { row, nested } = styles;
    if (row === undefined || nested === undefined) throw new Error("缺少设置行的样式类");
    const rowOf = (control: HTMLElement) => control.closest(`.${row}`);
    expect(rowOf(screen.getByRole("combobox", { name: "渐变时长" }))?.classList.contains(nested)).toBe(true);
    expect(rowOf(toggle)?.classList.contains(nested)).toBe(false);
  });

  it("plainLabel 时名称只是文字，不改按钮自己的名称", () => {
    render(
      <SettingRow label="处理顺序" plainLabel>
        <button type="button">对调处理顺序</button>
      </SettingRow>,
    );
    expect(screen.getByRole("button", { name: "对调处理顺序" })).toBeTruthy();
    expect(screen.getByText("处理顺序").tagName).toBe("SPAN");
  });
});
