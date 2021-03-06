package util

import (
	"crypto/rand"
	"encoding/binary"
	"bytes"
	"strings"
	"net"
	"errors"
	"sync"
	"time"
)

var Dns_address *net.UDPAddr
var domain_map = new(sync.Map)
var ip_domain_map *sync.Map

type domain_record struct {
	Ip   []byte
	Time int64
}

func init() {

	if Config.Client.Local_proxy && Config.Client.Domain_cache_time > 0 {
		const (
			check_ip_domain_expire_interval = 2 * 60
			number_of_ip_domain_pre_check   = 200
		)

		ip_domain_map = new(sync.Map)

		go func() {

			for {
				time.Sleep(check_ip_domain_expire_interval * time.Second)
				i := 0
				ip_domain_map.Range(func(key, value interface{}) bool {
					if i > number_of_ip_domain_pre_check {
						return false
					}
					if time.Now().Unix()-value.(*domain_record).Time >= Config.Client.Domain_cache_time {
						ip_domain_map.Delete(key)
					}
					i += 1
					return true
				})

			}
		}()
	}

}

const (
	A_record    = 1
	AAAA_record = 28
)

type DNSStruct struct {
	//header
	ID      uint16
	Flag    uint16
	QDCount uint16
	ANCount uint16
	NSCount uint16
	ARCount uint16

	//question

	Qname  []byte
	Qtype  uint16
	Qclass uint16

	data []byte
}

//type DNSAnswer struct{
//	Name     []byte
//	Type     uint16
//	Class    uint16
//	TTL      uint32
//	RDLength uint16
//	RData    []byte
//}

func (this *DNSStruct) fill_header() {
	_rand_byte := make([]byte, 2)
	rand.Read(_rand_byte)
	this.ID = uint16(_rand_byte[0] + _rand_byte[1])
	this.Flag = 1 << 8
	this.QDCount = 1
	this.ANCount = 0
	this.NSCount = 0
	this.ARCount = 0

}

func (this *DNSStruct) Fill_question(domain string, qtype uint16) {

	this.fill_header()
	var qname []byte
	for _, i := range strings.Split(domain, ".") {

		length := make([]byte, 2)
		binary.BigEndian.PutUint16(length, uint16(len([]rune(i))))
		qname = bytes.Join([][]byte{qname, length[1:], []byte(i)}, nil)

	}
	qname = append(qname, 0)
	this.Qname = qname
	this.Qtype = qtype
	this.Qclass = 1

}

func (this *DNSStruct) Marshal_request() []byte {
	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[:2], this.ID)
	binary.BigEndian.PutUint16(header[2:4], this.Flag)
	binary.BigEndian.PutUint16(header[4:6], this.QDCount)
	binary.BigEndian.PutUint16(header[6:8], this.ANCount)
	binary.BigEndian.PutUint16(header[8:10], this.NSCount)
	binary.BigEndian.PutUint16(header[10:12], this.ARCount)
	question := this.Qname
	qtype := make([]byte, 2)
	qclass := make([]byte, 2)
	binary.BigEndian.PutUint16(qtype, this.Qtype)
	binary.BigEndian.PutUint16(qclass, this.Qclass)
	return bytes.Join([][]byte{header, question, qtype, qclass}, nil)
}

//func (this *DNSStruct) Get_rdata() ([]byte, error) {
//
//	con, err := net.DialUDP("udp", nil, Dns_address)
//
//	if err != nil {
//		return nil, err
//	}
//	question := this.Marshal_request()
//	con.Write(question)
//	answer := make([]byte, Udp_recv_buff)
//	i, err := con.Read(answer)
//
//	if err != nil {
//		return nil, err
//	}
//	if i < len(question)+12 {
//		return nil, errors.New("illegal dns response")
//	}
//
//	answer = answer[len(question):i]
//
//	return Get_record_from_answer(answer, this.Qtype)
//
//}

func Get_record_from_answer(answer []byte, answer_type uint16) ([]byte, error) {
	if len(answer) < 12 {
		return nil, errors.New("can not found record")
	}
	if answer[0]&0xc0 == 0xc0 {
		_type := binary.BigEndian.Uint16(answer[2:4])
		rdate_len := binary.BigEndian.Uint16(answer[10:12])

		answer = answer[12:]
		if len(answer) < int(rdate_len) {
			return nil, errors.New("rdate len illegal")
		} else {
			if _type == answer_type {
				return answer[:rdate_len], nil
			}
			return Get_record_from_answer(answer[rdate_len:], answer_type)
		}

	} else {
		for i, v := range answer {
			if v == 0 {
				if len(answer)-i < 12 {
					return nil, errors.New("rdate len illegal")
				} else {
					answer = answer[i:]

					if answer[0]&0xc0 == 0xc0 {
						_type := binary.BigEndian.Uint16(answer[2:4])
						rdate_len := binary.BigEndian.Uint16(answer[10:12])
						answer = answer[12:]
						if len(answer) < int(rdate_len) {
							return nil, errors.New("rdate len illegal")
						}
						if _type == answer_type {
							return answer[:rdate_len], nil
						}
						return Get_record_from_answer(answer[rdate_len:], answer_type)

					} else {
						_type := binary.BigEndian.Uint16(answer[:2])

						rdate_len := binary.BigEndian.Uint16(answer[8:10])
						answer = answer[10:]

						if len(answer) < int(rdate_len) {
							return nil, errors.New("rdate len illegal")
						} else {
							if _type == answer_type {
								return answer[:rdate_len], nil
							}
							return Get_record_from_answer(answer[rdate_len:], answer_type)
						}
					}
				}
			}
		}
		return nil, errors.New("can not found record")
	}

}

func Get_domain_name_from_request(request []byte) string {
	defer Handle_panic()

	if len(request) < 14 {
		return ""
	}
	req := request[12:]

	for i, v := range req {
		if v == 0 {
			name := req[:i]
			if len(name) < 2 {
				return ""
			}

			domain := ""
			for {
				i := int(name[0])

				if len(name)-1 < i {
					return ""
				}
				domain += string(name[1:i+1]) + "."
				name = name[i+1:]
				if len(name) <= 1 {
					if len(domain) > 0 {
						domain = domain[:len(domain)-1]
					}
					return domain
				}

			}
		}
	}
	return ""

}

func Is_domain(url string) bool {
	if net.ParseIP(url) == nil {
		return true
	} else {
		return false
	}

}

func Parse_not_cn_domain(domain string, tcp_crypt, udp_crypt Crypt_interface) ([]byte, error) {
	if Config.Client.Domain_cache_time > 0 {
		if ip, ok := ip_domain_map.Load(domain); ok {
			return ip.(*domain_record).Ip, nil
		}
	}

	var ip []byte

	defer func() {
		if Config.Client.Domain_cache_time > 0 && len(ip) != 0 {
			go func() {
				ip_domain_map.Store(domain, &domain_record{
					Ip:   ip,
					Time: time.Now().Unix(),
				})
			}()
		}
	}()
	switch Config.Client.Dns_req_proto {
	case "tcp":
		con, raw, err := Connect_to_server(tcp_crypt, Udp_conn, Dns_address.Port, Dns_address.IP)
		if err != nil {
			return nil, err
		}
		defer Close_tcp(raw)

		dns := &DNSStruct{}

		var forward_dns_request = func(qtype uint16) ([]byte, error) {
			dns.Fill_question(domain, qtype)
			request := dns.Marshal_request()
			if err := tcp_crypt.Write(con, request); err != nil {
				return nil, err
			}
			if err := con.SetReadDeadline(time.Now().Add(time.Duration(Config.Udp_timeout) * time.Second)); err != nil {
				return nil, err
			}
			answer, err := tcp_crypt.Read(con)

			if answer != nil && len(answer) > len(request) {
				if bytes.Equal(request[:2], answer[:2]) {
					ip, err = Get_record_from_answer(answer[len(request):], qtype)
					if err == nil {
						return ip, nil
					} else {
						return nil, err
					}
				} else {
					return nil, errors.New("get record fail,recv a unexpect response")
				}
			}
			if err != nil {
				return nil, err
			}
			return nil, errors.New("get record fail,recv a unexpect response")
		}

		if Config.Client.Ipv6 {
			ip, err := forward_dns_request(AAAA_record)
			if err != nil {
				Print_log("get AAAA record fail:" + err.Error() + ",will try A record")
				goto __ipv4
				return ip, nil
			} else {
				return ip, nil
			}
		}

	__ipv4:
		ip, err := forward_dns_request(A_record)
		if err != nil {
			return nil, err
		}
		return ip, nil

	case "udp":

		con, err := net.ListenUDP("udp", nil)
		if err != nil {
			return nil, err
		}

		defer con.Close()

		local_addr := con.LocalAddr().(*net.UDPAddr)

		dns_port := make([]byte, 2)
		local_port := make([]byte, 2)
		binary.BigEndian.PutUint16(dns_port, uint16(Dns_address.Port))
		binary.BigEndian.PutUint16(local_port, uint16(local_addr.Port))

		dns_ip := Dns_address.IP.To4()
		if dns_ip == nil {
			dns_ip = Dns_address.IP.To16()
		}
		local_ip := local_addr.IP.To4()
		if local_ip == nil {
			local_ip = local_addr.IP.To16()
		}
		dest_addr := bytes.Join([][]byte{dns_port, dns_ip}, nil)
		_local_addr := bytes.Join([][]byte{local_port, local_ip}, nil)

		dns := &DNSStruct{}

		var forward_dns_request = func(qtype uint16) ([]byte, error) {
			dns.Fill_question(domain, qtype)
			request := dns.Marshal_request()

			if _, err := con.WriteTo(udp_crypt.Encrypt(bytes.Join([][]byte{
				{byte(len(dest_addr))}, dest_addr,
				{byte(len(_local_addr))}, _local_addr,
				request}, nil)), &net.UDPAddr{
				IP:   net.ParseIP(Config.Client.Server_addr),
				Port: Config.Client.Server_port,
			}); err != nil {
				return nil, err
			}

			data := make([]byte, Udp_recv_buff)
			if err := con.SetReadDeadline(time.Now().Add(time.Duration(Config.Udp_timeout) * time.Second)); err != nil {
				return nil, err
			}
			for {
				if err := con.SetReadDeadline(time.Now().Add(time.Duration(Config.Udp_timeout) * time.Second)); err != nil {
					return nil, err
				}
				i, err := con.Read(data)

				if i > 0 {
					dec_data, err := udp_crypt.Decrypt(data[:i])

					if err != nil {
						Print_log("decrypt err:" + err.Error())
						continue
					}
					_, _, _, _, answer, err := Parse_udp_recv(dec_data)
					if err != nil {
						Print_log("parse udp recv fail :" + err.Error())
						continue
					}
					if len(answer) > len(request) && bytes.Equal(request[:2], answer[:2]) {
						return Get_record_from_answer(answer[len(request):], qtype)
					}
				}

				if err != nil {
					return nil, err
				}
			}

		}

		if Config.Client.Ipv6 {
			ip, err = forward_dns_request(AAAA_record)
			if err != nil {
				Print_log("get AAAA record fail:" + err.Error() + ",will try A record")
				goto ipv4
			}
			return ip, nil
		}
	ipv4:
		ip, err = forward_dns_request(A_record)
		return ip, err
	default:
		return nil, errors.New("unsport proto")
	}
}

func Is_china_domain(domain string) (bool, error) {

	_domain := strings.Split(domain, ".")
	if len(_domain) < 2 {
		return false, errors.New("domain name illegal")
	}

	if _domain[len(_domain)-1] == "cn" {
		return true, nil
	}

	if _, ok := domain_map.Load(strings.Join(_domain[len(_domain)-2:], ".")); ok {
		return true, nil
	}
	return false, nil

	//if is_cn, ok := domain_map.Load(strings.Join(_domain[len(_domain)-2:], ".")); ok {
	//	return is_cn.(bool), nil
	//}

	//china_domain, err := os.Open("dnsmasq-china-list")
	//if err != nil {
	//	Print_log("warnnig : open dnsmasq-china-list file fail : "+ err.Error())
	//	return false, nil
	//}
	//defer china_domain.Close()
	//
	//reader := bufio.NewReader(china_domain)
	//
	//for {
	//	line, _, err := reader.ReadLine()
	//	if err != nil {
	//		if err == io.EOF {
	//
	//			go func() {
	//				domain_map.Store(strings.Join(_domain[len(_domain)-2:], "."), false)
	//			}()
	//
	//			return false, nil
	//
	//		} else {
	//			return false, err
	//		}
	//	}
	//	if len(line) == 0 {
	//		continue
	//	}
	//	if line_spl := strings.Split(string(line), "/"); len(line_spl) < 2 {
	//
	//		continue
	//
	//	} else {
	//
	//		//if reg2.MatchString(domain) || reg.MatchString(domain) {
	//		if strings.Join(_domain[len(_domain)-2:], ".") == line_spl[1] {
	//			go func() {
	//				domain_map.Store(strings.Join(_domain[len(_domain)-2:], "."), true)
	//
	//			}()
	//			return true, nil
	//		}
	//	}
	//
	//}

}
