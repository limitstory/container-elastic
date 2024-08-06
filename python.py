import re
import matplotlib.pyplot as plt

# 텍스트 파일 경로
file_path = '/home/limitstory/cloud/elastic/memory.txt'

# 텍스트 파일에서 데이터 읽기
with open(file_path, 'r') as file:
    data_string = file.read()

# 데이터 추출
# "getMemoryArray:  [" 부분과 "]" 부분을 제거하고 나머지 숫자들만 추출합니다.
numbers_string = re.search(r'getMemoryArray:\s+\[(.*)\]', data_string).group(1)
 
# 숫자 문자열을 리스트로 변환
system_memory_data = [float(num) for num in numbers_string.split()]

# 그래프 그리기
plt.figure(figsize=(10, 5))
plt.plot(system_memory_data, label='System Memory Usage')
plt.xlabel('Time')
plt.ylabel('Memory Usage (%)')
plt.title('System Memory Usage Over Time')
plt.legend()
plt.grid(True)
plt.show()