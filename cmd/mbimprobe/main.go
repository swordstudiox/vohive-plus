// Command mbimprobe is a throwaway real-device harness. NOT part of the build.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	dev "github.com/swordstudiox/vohive-plus/internal/device"
	mbimcore "github.com/swordstudiox/vohive-plus/internal/mbim"
)

func main() {
	mode := "probe"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "scan" {
		runScan()
		return
	}
	if mode == "uicc" {
		runUICC()
		return
	}
	if mode == "auth" {
		runAuth()
		return
	}
	if mode == "simauth" {
		runSimAuth()
		return
	}
	runProbe()
}

// runSimAuth 验证"逻辑通道 + APDU"链路:用已知能开的 ISD-R 完整 AID 开出通道,
// 然后在通道上发真 APDU 选 USIM(部分 AID),确认 APDU 传输可用 + USIM 可选中。
// 两点成立 → 逻辑通道 AKA 可行(复用现有 APDU AUTHENTICATE 逻辑)。
// runSimAuth 验证智能选卡与 MBIM 逻辑通道 AKA。
// 直接获取 QMI 暴露的长 AID，打开逻辑通道并尝试发送 0x88 AUTHENTICATE 指令。
func runSimAuth() {
	node := "/dev/cdc-wdm2"
	if len(os.Args) > 2 {
		node = os.Args[2]
	}
	octx, ocancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ocancel()
	m := mbimcore.New(node, "auto")
	if err := m.Open(octx); err != nil {
		fmt.Println("Open 失败:", err)
		os.Exit(1)
	}
	defer m.Close()
	fmt.Println("== 测试智能选卡与逻辑通道 APDU (MBIM AKA) ==")

	aidStr, source, err := m.ResolveLogicalChannelAID("usim", "")
	if err != nil {
		fmt.Println("智能选卡提取 USIM 失败:", err)
		return
	}
	fmt.Printf("成功提取 USIM 长 AID: %s (Source: %s)\n", aidStr, source)

	aid, _ := hex.DecodeString(aidStr)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	ch, err := m.OpenChannel(ctx, aid)
	if err != nil {
		fmt.Printf("OpenChannel(USIM) 失败: %v\n", err)
		return
	}
	fmt.Printf("OpenChannel(USIM) OK channel=%d\n", ch)
	defer m.CloseChannel(ctx, ch)

	// 伪造全 0 的 RAND 和 AUTN 发送 AKA
	randBytes := make([]byte, 16)
	autnBytes := make([]byte, 16)
	// CLA(含通道号), INS(0x88), P1(0x00), P2(0x81), Lc(0x22), Data...
	cla := byte(ch) & 0x03
	apdu := []byte{cla, 0x88, 0x00, 0x81, 0x22, 0x10}
	apdu = append(apdu, randBytes...)
	apdu = append(apdu, 0x10)
	apdu = append(apdu, autnBytes...)

	fmt.Printf("发送 3G AKA 鉴权 APDU: %X\n", apdu)
	resp, err := m.TransmitAPDU(ctx, ch, apdu)
	if err != nil {
		fmt.Println("TransmitAPDU(AKA) 失败:", err)
		return
	}
	fmt.Printf("TransmitAPDU(AKA) 响应: %X\n", resp)
	if len(resp) >= 2 && resp[len(resp)-2] == 0x98 && resp[len(resp)-1] == 0x62 {
		fmt.Println("验证成功：收到 9862(MAC Failure)！底层逻辑通道鉴权完全打通。")
	} else if len(resp) >= 2 && resp[len(resp)-2] == 0x61 {
		fmt.Println("验证成功：收到 61xx！有响应数据。底层逻辑通道鉴权完全打通。")
	} else {
		fmt.Println("收到其他响应，可能是通道状态或指令参数不支持。")
	}
}

// runAuth 判别这颗模组的 MBIM AUTH 服务是真可用还是 stub。
//   - AUTH_SIM(2G GSM):无 AUTN/MAC,功能正常的子系统对任意 RAND 都返回 SRES/Kc;
//     报错 → 整个 AUTH 服务是 stub。
//   - AUTH_AKA(随机 RAND/AUTN):预期 status=0x23(35,INCORRECT_AUTN),作基线对照。
func runAuth() {
	node := "/dev/cdc-wdm2"
	if len(os.Args) > 2 {
		node = os.Args[2]
	}
	octx, ocancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ocancel()
	m := mbimcore.New(node, "auto")
	if err := m.Open(octx); err != nil {
		fmt.Println("Open 失败:", err)
		os.Exit(1)
	}
	defer m.Close()
	fmt.Println("== MBIM Open 成功，测试 AUTH 服务可用性 ==")

	r16 := func() []byte {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		return b
	}

	ctx1, c1 := context.WithTimeout(context.Background(), 8*time.Second)
	sres, kc, err := m.AuthSIM(ctx1, r16())
	c1()
	if err != nil {
		fmt.Printf("AUTH_SIM(2G): 报错 -> %v\n", err)
		fmt.Println("  判定: AUTH 服务大概率是 stub(连无 MAC 校验的 GSM 鉴权都失败)")
	} else {
		fmt.Printf("AUTH_SIM(2G): OK  SRES=0x%08x Kc=0x%016x\n", sres, kc)
		fmt.Println("  判定: AUTH 子系统是工作的 → AKA 的 status=35 不是 stub,需查应用上下文(USIM/ISIM)")
	}

	ctx2, c2 := context.WithTimeout(context.Background(), 8*time.Second)
	_, _, _, _, akaErr := m.CalculateAKA(ctx2, r16(), r16())
	c2()
	if akaErr != nil {
		fmt.Printf("AUTH_AKA(随机): %v  (基线:随机 AUTN 必然 MAC 失败,预期 0x23/35)\n", akaErr)
	} else {
		fmt.Println("AUTH_AKA(随机): 竟然成功? 异常,随机 AUTN 不应通过 MAC")
	}
}

func runScan() {
	// 复现 API handleDeviceMgmtDiscovered 的发现路径：QMI+fallback 合并。
	fmt.Println("== DiscoverCompatibleModemsFromQMI(nil)  [= /devices/discovered 路径] ==")
	list, err := dev.DiscoverCompatibleModemsFromQMI(nil)
	if err != nil {
		fmt.Println("发现错误:", err)
		return
	}
	if len(list) == 0 {
		fmt.Println("（未发现兼容 modem）")
		return
	}
	for i, m := range list {
		fmt.Printf("[%d] mode=%s transport=%s vid:pid=%04x:%04x driver=%s\n",
			i, m.Mode, m.TransportType, m.VendorID, m.ProductID, m.DriverName)
		fmt.Printf("    control=%s net=%s usb=%s netcap=%v IMEI=%s AT=%v\n",
			m.ControlPath, m.NetInterface, m.USBPath, m.NetworkCapable, m.IMEI, m.ATPorts)
		if m.Mode == "mbim" {
			enriched, imei := dev.EnrichDiscoveredCompatibleModem(m, dev.CompatibleModemEnrichOptions{
				EnableATProbe:  true,
				ATProbeTimeout: 900 * time.Millisecond,
			})
			fmt.Printf("    [enrich] AT口=%s IMEI=%q (enrichment 未崩溃)\n", enriched.ATPort, imei)
		}
	}
}

func runUICC() {
	node := "/dev/cdc-wdm2"
	if len(os.Args) > 2 {
		node = os.Args[2]
	}
	octx, ocancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ocancel()
	m := mbimcore.New(node, "auto")
	if err := m.Open(octx); err != nil {
		fmt.Println("Open 失败:", err)
		os.Exit(1)
	}
	defer m.Close()
	fmt.Println("== MBIM Open 成功（走 mbim-proxy，不与现有进程抢占设备），测试 UICC 逻辑通道 ==")

	// 尝试直接通过 QMI over MBIM 隧道获取长 AID 列表
	fmt.Println("== 尝试通过原生 QMI over MBIM 隧道获取所有应用信息 ==")
	qmiCtx, qmiCancel := context.WithTimeout(context.Background(), 5*time.Second)
	apps, qmiErr := m.QMIUIMApplicationList(qmiCtx)
	qmiCancel()
	if qmiErr != nil {
		fmt.Printf("QMIUIMApplicationList 失败: %v\n", qmiErr)
	} else {
		appTypeNames := map[uint8]string{1: "SIM(2G)", 2: "USIM(3G/4G/5G)", 3: "RUIM", 4: "CSIM", 5: "ISIM"}
		for i, app := range apps {
			name := appTypeNames[app.Type]
			if name == "" {
				name = fmt.Sprintf("Unknown(%d)", app.Type)
			}
			fmt.Printf("  发现应用[%d]: 类型=%-14s AID=%X\n", i, name, app.AID)
		}
	}
	fmt.Println("==================================================")

	// ISD-R AID (eSIM) 和 USIM 选 MF 的最小尝试
	cases := []struct {
		name string
		aid  []byte
	}{
		{"ISD-R(eSIM)", []byte{0xA0, 0x00, 0x00, 0x05, 0x59, 0x10, 0x10, 0xFF, 0xFF, 0xFF, 0xFF, 0x89, 0x00, 0x00, 0x01, 0x00}},
		{"USIM Partial(7)", []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02}},
		{"USIM Fake(16)", []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
		{"空AID(基础通道)", nil},
	}
	for _, c := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		ch, err := m.OpenChannel(ctx, c.aid)
		if err != nil {
			fmt.Printf("%-16s OpenChannel 失败: %v\n", c.name, err)
			cancel()
			continue
		}
		fmt.Printf("%-16s OpenChannel OK channel=%d\n", c.name, ch)
		_ = m.CloseChannel(ctx, ch)
		cancel()
	}
}

func runProbe() {
	node := "/dev/cdc-wdm2"
	if len(os.Args) > 2 {
		node = os.Args[2]
	}
	octx, ocancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ocancel()
	m := mbimcore.New(node, "direct")
	if err := m.Open(octx); err != nil {
		fmt.Println("Open 失败:", err)
		os.Exit(1)
	}
	defer m.Close()
	fmt.Println("== MBIM Open 成功 ==")

	step := func(name string, fn func(context.Context) string) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		done := make(chan string, 1)
		go func() { done <- fn(ctx) }()
		select {
		case s := <-done:
			fmt.Printf("%-14s %s\n", name+":", s)
		case <-ctx.Done():
			fmt.Printf("%-14s 超时/无响应\n", name+":")
		}
	}

	step("DeviceCaps", func(ctx context.Context) string {
		c, err := m.DeviceCaps(ctx)
		if err != nil {
			return "ERR " + err.Error()
		}
		return fmt.Sprintf("IMEI=%s FW=%s", c.DeviceID, c.FirmwareInfo)
	})
	step("Subscriber", func(ctx context.Context) string {
		s, err := m.SubscriberReady(ctx)
		if err != nil {
			return "ERR " + err.Error()
		}
		return fmt.Sprintf("ready=%d IMSI=%s ICCID=%s", s.ReadyState, s.IMSI, s.ICCID)
	})
	step("Register", func(ctx context.Context) string {
		r, err := m.RegisterState(ctx)
		if err != nil {
			return "ERR " + err.Error()
		}
		return fmt.Sprintf("state=%d provider=%q MCC=%s MNC=%s", r.RegisterState, r.ProviderName, r.MCC, r.MNC)
	})
	step("Signal", func(ctx context.Context) string {
		s, err := m.SignalState(ctx)
		if err != nil {
			return "ERR " + err.Error()
		}
		return fmt.Sprintf("unknown=%v rssi=%d dBm=%d", s.Unknown, s.RSSI, s.DBM)
	})
	step("Packet", func(ctx context.Context) string {
		p, err := m.PacketService(ctx)
		if err != nil {
			return "ERR " + err.Error()
		}
		return fmt.Sprintf("state=%d", p.State)
	})
	step("Radio", func(ctx context.Context) string {
		r, err := m.RadioState(ctx)
		if err != nil {
			return "ERR " + err.Error()
		}
		return fmt.Sprintf("hw=%d sw=%d", r.Hardware, r.Software)
	})
}
